# lambda-builder

A tool for building lamda functions into uploadable zip files via Docker based on work from [@lambci](https://github.com/lambci/docker-lambda) and [@mLupine](https://github.com/mLupine/docker-lambda).

## Building

```shell
# substitute the version number as desired
go build -ldflags "-X main.Version=0.1.0
```

## Dependencies

- The `docker` binary

## Usage

```
Usage: lambda-builder [--version] [--help] <command> [<args>]

Available commands are:
    build      Builds a lambda function
    version    Return the version of the binary
```

To build an app:

```shell
cd path/to/app

# will write a lambda.zip in the current working directory
lambda-builder build
```

Alternatively, a given path can be specified via the `--working-directory` flag:

```shell
# will write a lambda.zip in the specified path
lambda-builder build --working-directory path/to/app
```

### How does it work

Internally, `lambda-builder` detects a given language and builds the app according to the script specified by the detected builder within a disposablecontainer environment emulating AWS Lambda. If a builder is not detected, the build will fail. The following languages are supported:

- `dotnet`
  - default build image: `mlupin/docker-lambda:dotnet6-build`
  - requirement: `Function.cs`
  - runtimes:
    - dotnet6
    - dotnetcore3.1
- `go`
  - default build image: `lambci/lambda:build-go1.x`
  - requirement: `go.mod`
  - runtimes:
    - provided.al2
- `nodejs`
  - default build image: `mlupin/docker-lambda:nodejs14.x-build`
  - requirement: `package-lock.json`
  - runtimes:
    - nodejs12.x
    - nodejs14.x
- `python`
  - default build image: `mlupin/docker-lambda:python3.9-build`
  - requirement: `requirements.txt`, `poetry.lock`, or `Pipfile.lock`
  - notes: Autodetects the python version from `poetry.lock`, `Pipfile.lock`, or `runtime.txt`
  - runtimes:
    - python3.8
    - python3.9
- `ruby`
  - default build image: `mlupin/docker-lambda:ruby2.7-build`
  - requirement: `Gemfile.lock`
  - runtimes:
    - ruby2.7

When the app is built, a `lambda.zip` will be produced in the specified working directory. The resulting `lambda.zip` can be uploaded to S3 and used within a Lambda function.

Both the builder and the build image environment can be overriden in an optional `lambda.yml` file in the specified working directory. An example of this file is as follows:

```yaml
---
build_image: mlupin/docker-lambda:dotnetcore3.1-build
builder: dotnet
```

- `build_image`: A docker image that is accessible by the docker daemon. The `build_image` _should_ be based on an existing Lambda image - builders may fail if they cannot run within the specified `build_image`. The build will fail if the image is inaccessible by the docker daemon.
- `builder`: The name of a builder. This may be used if multiple builders match and a specific builder is desired. If an invalid builder is specified, the build will fail.

### Deploying

The `lambda.zip` file can be directly uploaded to a lambda function and used as is by specifying the correct runtime. See the `test.bats` files in any of the `test` examples for more info on how to perform this with the `awscli` (v2).

## Examples

See the `tests` directory for examples on how to use this project.
