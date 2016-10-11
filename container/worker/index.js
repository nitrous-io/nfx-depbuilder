const fs = require('fs'),
      path = require('path'),
      spawn = require('child_process').spawn,
      readline = require('readline'),
      aws = require('aws-sdk'),
      tmp = require('tmp'),
      archiver = require('archiver'),
      async = require('async');

const processNextMessage = () => {
  aws.config.region = process.env.AWS_REGION;

  const sqs = new aws.SQS({ apiVersion: '2012-11-05' }),
        s3 = new aws.S3({ apiVersion: '2006-03-01' }),
        cloudwatchlogs = new aws.CloudWatchLogs({ apiVersion: '2014-03-28' });;

  var bucket, receiptHandle;

  async.waterfall([
    (next) => {
      const params = {
        QueueUrl: process.env.SQS_QUEUE_URL,
        AttributeNames: ['All'],
        MaxNumberOfMessages: 1,
        WaitTimeSeconds: 20 // 20 seconds is the max allowed by AWS.
      }
      sqs.receiveMessage(params, (err, data) => {
        if (err) {
          return next(err);
        }

        if (!data.Messages) {
          return next(new Error('No messages in queue'));
        }

        next(null, data.Messages[0]);
      });
    },
    (msg, next) => {
      console.log('Processing message:', msg);

      const b = JSON.parse(msg.Body),
            jobId = b.job_id,
            key = b.key;

      bucket = b.bucket;
      receiptHandle = msg.ReceiptHandle;

      if (!jobId) {
        return next(new Error('Message has no job ID: ' + msg));
      }

      console.log(`Processing new job from S3 bucket '${bucket}' with key '${key}', job ID: '${jobId}'...`);

      s3.getObject({ Bucket: bucket, Key: key }, (err, data) => {
        if (err) {
          return next(err);
        }

        const contents = data.Body.toString();

        next(null, jobId, contents);
      });
    },
    (jobId, contents, next) => {
      const workDir = tmp.dirSync();
      console.log(`Created temporary directory at '${workDir.name}'`);
      const pkgJsonPath = path.join(workDir.name, 'package.json');
      fs.writeFile(pkgJsonPath, contents, (err) => {
        if (err) {
          return next(err);
        }

        console.log(`Saved package.json locally at '${pkgJsonPath}'`);

        next(null, jobId, workDir);
      });
    },
    (jobId, workDir, next) => {
      const logPath = path.join(workDir.name, 'npm-install.log'),
            logStream = fs.createWriteStream(logPath, { flags: 'a+' });

      console.log('Running npm install...');
      const npmInstall = spawn('npm', ['install', '--production'], {
        cwd: workDir.name,
        shell: '/bin/bash',
        stdio: ['ignore', 'pipe', 'pipe']
      });

      // Send stdout/stderr to both the parent process' stdout/stderr and to a
      // regular file.
      npmInstall.stdout.pipe(logStream);
      npmInstall.stdout.pipe(process.stdout);
      npmInstall.stderr.pipe(logStream);
      npmInstall.stderr.pipe(process.stderr);

      // Save output so that we can send them to CloudWatch logs.
      const logs = [];
      const collectLogs = (data) => {
        if (data.toString() === '') return;

        logs.push({ message: data.toString(), timestamp: Date.now() });
      };

      // Use readline to properly split logs line-by-line.
      readline.createInterface({ input: npmInstall.stdout }).on('line', collectLogs);
      readline.createInterface({ input: npmInstall.stderr }).on('line', collectLogs);

      var stopLogging = false;

      npmInstall.on('close', (code) => {
        console.log('npm install exited with code:', code);
        logStream.end();

        if (code !== 0) {
          throw('npm install failed with exit code:' + code);
        }

        // Zip up node_modules/ directory and upload to S3.
        const zipPath = path.join(workDir.name, 'artifact.zip'),
              zipStream = fs.createWriteStream(zipPath),
              archive = archiver('zip');

        archive.on('end', () => {
          console.log('zip file created at', zipPath, ', size:', archive.pointer(), 'bytes');

          async.waterfall([
            (next) => {
              // Upload result zipfile to S3.
              const resultKey = getResultKey(jobId);
              const params = {
                Bucket: bucket,
                Key: resultKey,
                Body: fs.createReadStream(zipPath)
              };
              s3.putObject(params, (err, data) => {
                if (err) {
                  console.warn('Error uploading job result to S3:', err);
                } else {
                  console.log('Uploaded', zipPath, 'to S3 with key', resultKey);
                }

                next(err)
              });
            },
            (next) => {
              // Update job's status.json with a "state" of "completed".
              const statusKey = `jobs/${jobId}/status.json`;
              const statusBody = JSON.stringify({
                id: jobId,
                state: 'completed'
              });

              const params = { Bucket: bucket, Key: statusKey, Body: statusBody };
              s3.putObject(params, (err, data) => {
                if (err) {
                  console.warn(`Error updating job status on S3: ${err}`);
                } else {
                  console.log('Updated job status to completed on S3');
                }

                next(err);
              });
            },
            (next) => {
              // Delete message from queue (indicates that job was
              // successfully processed).
              const params = {
                ReceiptHandle: receiptHandle,
                QueueUrl: process.env.SQS_QUEUE_URL
              };
              sqs.deleteMessage(params, (err, data) => {
                if (err) {
                  console.warn(`Error deleting message ${msg.MessageId} from queue: ${err}`);
                }

                next(err);
              });
            }
          ], (err) => {
            if (err) {
              throw(err);
            }
          })
        });

        archive.on('error', (err) => {
          console.warn('Error zipping up build directory:', err);
          return next(err);
        });

        archive.pipe(zipStream)
        archive.directory(path.join(workDir.name, 'node_modules'), '/node_modules')
        archive.finalize();

        // Wait for remaining logs (if any) to be pushed to CloudWatch logs.
        const timer = setInterval(() => {
          if (logs.length === 0) {
            stopLogging = true;
            clearInterval(timer);
          }
        }, 1000);
        setTimeout(() => {
          stopLogging = true;
          clearInterval(timer);
        }, 10000); // Wait for logs for at most 10 seconds.
      });

      npmInstall.on('error', (err) => {
        console.warn('Error starting npm install:', err);
        return next(err);
      });

      const logStreamName = generateLogStreamName(jobId);
      console.log(`Logging output to new log stream '${logStreamName}'`);
      cloudwatchlogs.createLogStream({
        logGroupName: process.env.RESPONSE_LOG_GROUP,
        logStreamName
      }, (err, data) => {
        if (err) {
          console.warn('Error creating log stream:', err);
          return;
        }

        var sequenceToken;
        var inFlight = false;

        const timer = setInterval(() => {
          if (stopLogging) {
            clearInterval(timer);
          }

          // Prevent this callback from running concurrently, since CloudWatch
          // API requires a sequenceToken from the previous putLogEvents.
          if (inFlight) return;

          const logEvents = logs.splice(0, logs.length);
          if (logEvents.length === 0) {
            return;
          }

          inFlight = true;

          const params = {
            logGroupName: process.env.RESPONSE_LOG_GROUP,
            logEvents,
            logStreamName,
            sequenceToken
          };
          cloudwatchlogs.putLogEvents(params, (err, data) => {
            if (err) {
              console.warn('Error writing npm output to CloudWatch logs:', err);
            } else {
              sequenceToken = data.nextSequenceToken;
              inFlight = false;
            }
          });
        }, 500);
      });
    }
  ], (err) => {
    if (err) {
      throw(err);
    }
  })
};

function generateLogStreamName(jobId) {
  return `npm-install-log/${jobId}`;
}

function getResultKey(jobId) {
  return `jobs/${jobId}/artifact.zip`;
}

if (require.main === module) {
  processNextMessage();
}

exports.default = processNextMessage;
