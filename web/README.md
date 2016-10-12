### Web server for nfx-depbuilder

This is a Golang web server that provides an HTTP API to nfx-depbuilder.

#### Development

Run `go webserver/main.go`.

You'll need to export some environment variables as well.

#### Releasing

`make release` will build a Docker image (tagged as
`nfx-depbuilder-web:latest`) that contains the web server binary.

Now, you have to figure out how to deploy the web server as a Docker container.

#### Vendoring dependencies

We use [govendor](https://github.com/kardianos/govendor) to vendor dependencies.
