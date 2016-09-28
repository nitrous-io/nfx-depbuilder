const fs = require('fs'),
      aws = require('aws-sdk'),
      async = require('async');

const sqs = new aws.SQS({apiVersion: '2012-11-05'}),
      ecs = new aws.ECS({apiVersion: '2014-11-13'});

exports.handler = (event, context, callback) => {
  console.log('Received event:', JSON.stringify(event, null, 2));

  var ENV = {};

  const bucket = event.Records[0].s3.bucket.name;
  const key = decodeURIComponent(event.Records[0].s3.object.key.replace(/\+/g, ' '));

  async.waterfall([
    (next) => {
      fs.readFile(`${process.env.LAMBDA_TASK_ROOT}/env.json`, 'utf8', (err, data) => {
        if (err) {
          console.warn('Error loading environment from env.json:', err);
        } else {
          ENV = JSON.parse(data);
          console.log('Env:', ENV);
        }
        next(err);
      });
    },
    (next) => {
      const message = { bucket, key };
      const params = {
        MessageBody: JSON.stringify(message),
        QueueUrl: ENV.SQS_QUEUE_URL
      };

      sqs.sendMessage(params, (err, data) => {
        if (err) {
          console.warn('Error sending message to SQS queue:', err);
        } else {
          console.info('Message sent to SQS queue, ID:', data.MessageId);
        }
        next(err);
      });
    },
    (next) => {
      const params = {
        taskDefinition: ENV.TASK_DEF_ARN,
        cluster: ENV.TASK_CLUSTER_NAME,
        count: 1,
        startedBy: 'nfx-depbuilder-lambda'
      };

      ecs.runTask(params, (err, data) => {
        if (err) {
          console.warn('Error starting worker task:', err);
        } else {
          if (data.failures && data.failures.length > 0) {
            console.warn("Failure(s) starting worker task:");
            for (var i = 0; i < data.failures.length; i++) {
              console.warn(data.failures[i].reason);
            }
            next("Failure(s) starting worker task");
            return;
          }

          console.info('Worker task started on container instance with ARN:', data.tasks[0].containerInstanceArn);
        }
        next(err);
      });
    }
  ], (err) => {
    if (err) {
      callback('An error has occurred: ' + err);
    } else {
      callback(null, 'this should contain a URL to the location of the result or status');
    }
  });
};
