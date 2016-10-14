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
2. **Push Builder Docker Image**: Push the builder Docker image to the ECR
   repository.
3. **Push Web Docker Image**: Push the web server Docker image to the ECR
   repository.
5. **Update Stack**: Update the CloudFormation stack to setup the ECS (EC2
   Container Service) components, etc.

### 1. Initialize Stack

We create a CloudFormation stack with only:
* an ECR repository for the builder Docker image.
* another ECR repository for our web server Docker image.

Steps:

1. Create a new CloudFormation stack using
   [cloudformation-stack-step-1.json](cloudformation-stack-step-1.json). You
   can use either the
   [AWS Management Console](https://console.aws.amazon.com/cloudformation/home)
   or the AWS CLI to do this.
2. Take note of these `Outputs` from the stack that will be used in subsequent
   steps:
  * `BuilderDockerRepositoryName`
  * `WebDockerRepositoryName`

### 2. Push Builder Docker Image

Prerequisites:

1. Docker
2. [AWS CLI](http://docs.aws.amazon.com/cli/latest/userguide/installing.html)

Steps:

1. Install dependencies for the builder app (using [Yarn](https://yarnpkg.com/)):
   `cd builder && yarn install --production`
* Build the builder Docker image specified in [`builder/Dockerfile`](builder/Dockerfile):
  `make release`
* Retrieve the `docker login` command that you can use to authenticate your
   Docker client to the Amazon ECR registry:
   `aws ecr get-login --region <AWS_REGION>`
* Run the `docker login` command returned from the previous step.
* Tag the Docker image with the Amazon ECR registry/repository:
   `docker tag nfx-depbuilder <AWS_ACCOUNT_ID>.dkr.ecr.region.amazonaws.com/<BuilderDockerRepositoryName>:latest`
* Push the image to the AWS repository:
   `docker push <AWS_ACCOUNT_ID>.dkr.ecr.region.amazonaws.com/<BuilderDockerRepositoryName>:latest`

### 3. Push Web Docker Image

Steps:

1. Build the web server Docker image specified in [`web/Dockerfile`](web/Dockerfile):
   `cd web && make release`
* Retrieve the `docker login` command that you can use to authenticate your
   Docker client to the Amazon ECR registry:
   `aws ecr get-login --region <AWS_REGION>`
* Run the `docker login` command returned from the previous step.
* Tag the Docker image with the Amazon ECR registry/repository:
   `docker tag nfx-depbuilder-web <AWS_ACCOUNT_ID>.dkr.ecr.region.amazonaws.com/<WebDockerRepositoryName>:latest`
* Push the image to the AWS repository:
   `docker push <AWS_ACCOUNT_ID>.dkr.ecr.region.amazonaws.com/<WebDockerRepositoryName>:latest`

### 4. Update Stack

This step will fully provision the dependency builder stack.

Steps:

1. Update the CloudFormation stack from step 1 using
   [cloudformation-stack-step-2.json](cloudformation-stack-step-2.json).
2. Specify the CloudFormation template parameters:
  * Pick a `KeyPair` so that we can SSH into the EC2 instances of the ECS
    cluster (e.g. for debugging).
3. Wait for the update to complete - it'll take a few minutes to be fully
   provisioned.
