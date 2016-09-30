# nfx-depbuilder

nfx-depbuilder is a 100% AWS-hosted service that builds dependencies for the NFX
CLI. It installs NPM modules in an environment exactly like (or very similar to)
the [AWS Lambda execution environment](http://docs.aws.amazon.com/lambda/latest/dg/current-supported-versions.html). It also supports a "pre-`npm install`" step where system dependencies
can be installed (e.g. to install a dependency that a particular NPM module
requires).

> **depbuilder**<br>
> _noun_
>   1. the lazy programmer's way of saying "dependency builder"
>
> _Origin_<br>
> A lazy programmer

## Setting up on AWS

Setting up the nfx-depbuilder stack on AWS is a 4-step process:

1. **Initialize Stack**: Create CloudFormation stack with an S3 bucket (for
   Lambda function) and an ECR (EC2 Container Registry) repository (for our
   Docker image).
2. **Upload Lambda Function**: Upload zipfile containing Lambda function to
   the S3 bucket.
3. **Push Docker Image**: Push our Docker image to the ECR repository.
4. **Update Stack**: Update the CloudFormation stack to setup the Lambda
   function, ECS (EC2 Container Service) components, etc.

### 1. Initialize Stack

We create a CloudFormation stack with only:
* an S3 bucket - we will upload a zipfile containing our Lambda function here.
* an ECR repository - we will push our Docker image that does the `npm install`
  here.

Steps:

1. Create a new CloudFormation stack using
   [cloudformation-stack-step-1.json](cloudformation-stack-step-1.json). You
   can use either the
   [AWS Management Console](https://console.aws.amazon.com/cloudformation/home)
   or the AWS CLI to do this.
2. Take note of the `Outputs` from the stack:
  * `LambdaBucketName`: S3 bucket that will be used in step 2.
  * `DockerRepositoryName`: Docker repository that will be used in step 3.
  * `JobQueueURL`, `ECSTaskDefinitionARN`, `ECSClusterName`: these are required
     to configure the `worker-launcher` Lambda function.

### 2. Upload worker-launcher Lambda function

Steps:

1. Copy the Outputs from step 1 into [`env.json`](worker-launcher/env.json).
  * _TODO_: create a script that does this step automatically.
2. Zip up the [`worker-launcher`](worker-launcher/) directory.
  * Make sure the directory contents are at the root of the zipfile.
  * `cd worker-launcher; zip -r worker-launcher.zip ./`
3. Upload the zipfile to the `LambdaBucketName` S3 bucket created in the
   previous step.

### 3. Push Docker image

Prerequisites:

1. You need Docker and the [AWS CLI](http://docs.aws.amazon.com/cli/latest/userguide/installing.html)
   installed.
2. You should build the Docker image specified in [`container/Dockerfile`](container/Dockerfile).

Steps:

1. Retrieve the `docker login` command that you can use to authenticate your
   Docker client to the Amazon ECR registry:
   `aws ecr get-login --region <AWS_REGION>`
2. Run the `docker login` command returned from the previous step.
3. Tag the Docker image with the Amazon ECR registry/repository:
   `docker tag nfx-depbuilder <AWS_ACCOUNT_ID>.dkr.ecr.region.amazonaws.com/<DockerRepositoryName>:latest`
4. Push the image to the AWS repository:
   `docker push <AWS_ACCOUNT_ID>.dkr.ecr.region.amazonaws.com/<DockerRepositoryName>:latest`

### 4. Update Stack

This step will fully provision the dependency builder stack.

Steps:

1. Update the CloudFormation stack from step 1 using
   [cloudformation-stack-step-2.json](cloudformation-stack-step-2.json).
2. Specify the CloudFormation template parameters:
  * Use the S3 key of the Lambda function zipfile from step 2 (probably
    `worker-launcher.zip`).
  * Pick a `KeyPair` so that we can SSH into the EC2 instances of the ECS
    cluster (e.g. for debugging).
3. Wait for the update to complete - it'll take a few minutes to be fully
   provisioned.
