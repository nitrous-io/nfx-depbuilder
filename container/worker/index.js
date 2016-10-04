const fs = require('fs'),
      path = require('path'),
      spawn = require('child_process').spawn,
      readline = require('readline'),
      aws = require('aws-sdk'),
      tmp = require('tmp');

const processNextMessage = () => {
  aws.config.region = process.env.AWS_REGION;

  const sqs = new aws.SQS({ apiVersion: '2012-11-05' }),
        s3 = new aws.S3({ apiVersion: '2006-03-01' }),
        cloudwatchlogs = new aws.CloudWatchLogs({ apiVersion: '2014-03-28' });;

  const params = {
    QueueUrl: process.env.SQS_QUEUE_URL,
    AttributeNames: ['All'],
    MaxNumberOfMessages: 1,
    WaitTimeSeconds: 20 // 20 seconds is the max allowed by AWS.
  }
  sqs.receiveMessage(params, (err, data) => {
    if (err) {
      throw('Error receiving message from queue:' + err);
    }

    if (!data.Messages) {
      console.warn(`No messages in queue, exiting.`)
      process.exit(0)
    }

    const msg = data.Messages[0],
          receiptHandle = msg.ReceiptHandle;
    console.log('Received message:', msg);

    const b = JSON.parse(msg.Body),
          bucket = b.bucket,
          key = b.key,
          jobId = `${key}-${Date.now()}`;

    console.log(`Processing new job from S3 bucket '${bucket}' with key '${key}', job ID: '${jobId}'...`);

    const tmpDir = tmp.dirSync();
    console.log(`Created temporary directory at '${tmpDir.name}'`);
    const pkgJsonPath = path.join(tmpDir.name, 'package.json');
    const ws = fs.createWriteStream(pkgJsonPath);

    const pipe = s3.getObject({ Bucket: bucket, Key: key })
      .createReadStream().pipe(ws);
    pipe.on('finish', () => {
      console.log(`Saved package.json locally at '${pkgJsonPath}'`);

      const logPath = path.join(tmpDir.name, 'npm-install.log'),
            logStream = fs.createWriteStream(logPath, { flags: 'a+' });

      console.log('Running npm install...');
      const npmInstall = spawn('npm', ['install', '--production'], {
        cwd: tmpDir.name,
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

        if (code === 0) {
          // Delete message from queue (indicates that job was successfully
          // processed).
          const params = {
            ReceiptHandle: receiptHandle,
            QueueUrl: process.env.SQS_QUEUE_URL
          };
          sqs.deleteMessage(params, (err, data) => {
            if (err) {
              console.warn(`Error deleting message ${msg.MessageId} from queue: ${err}`)
            }
          });
        }

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
        throw('Error starting npm install:', err);

        // TODO Record and signal error, e.g. so that we get alerted via a
        // CloudWatch alarm.
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
    });
  });
};

function generateLogStreamName(jobId) {
  return `npm-install-log/${jobId}`;
}

if (require.main === module) {
  processNextMessage();
}

exports.default = processNextMessage;
