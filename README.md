# Rest Server


[![Status badge for tests](https://github.com/happal/monsoon/workflows/Build%20and%20tests/badge.svg)](https://github.com/happal/monsoon/actions?query=workflow%3A%22Build+and+tests%22)
[![Status badge for style checkers](https://github.com/happal/monsoon/workflows/Style%20Checkers/badge.svg)](https://github.com/happal/monsoon/actions?query=workflow%3A%22Style+Checkers%22)
[![Go Report Card](https://goreportcard.com/badge/github.com/restic/rest-server)](https://goreportcard.com/report/github.com/restic/rest-server)
[![GoDoc](https://godoc.org/github.com/restic/rest-server?status.svg)](https://godoc.org/github.com/restic/rest-server)
[![License](https://img.shields.io/badge/license-BSD%20%282--Clause%29-003262.svg?maxAge=2592000)](https://github.com/restic/rest-server/blob/master/LICENSE)
[![Powered by](https://img.shields.io/badge/powered_by-Go-5272b4.svg?maxAge=2592000)](https://golang.org/)

Rest Server is a high performance HTTP server that implements restic's [REST backend API](https://restic.readthedocs.io/en/latest/100_references.html#rest-backend).  It provides secure and efficient way to backup data remotely, using [restic](https://github.com/restic/restic) backup client via the [rest: URL](https://restic.readthedocs.io/en/latest/030_preparing_a_new_repo.html#rest-server).

## Requirements

Rest Server requires Go 1.11 or higher to build.  The only tested compiler is the official Go compiler.  Building server with `gccgo` may work, but is not supported.

The required version of restic backup client to use with `rest-server` is [v0.7.1](https://github.com/restic/restic/releases/tag/v0.7.1) or higher.

## Build

For building the `rest-server` binary run `CGO_ENABLED=0 go build -o rest-server ./cmd/rest-server`

## Docker

### Build image

Put the `rest-server` binary in the current directory, then run:

    docker build -t restic/rest-server:latest .


### Pull image

    docker pull restic/rest-server

## Usage

To learn how to use restic backup client with REST backend, please consult [restic manual](https://restic.readthedocs.io/en/latest/030_preparing_a_new_repo.html#rest-server).

    $ rest-server --help

    Run a REST server for use with restic

    Usage:
      rest-server [flags]

    Flags:
          --append-only          enable append only mode
          --cpu-profile string   write CPU profile to file
          --debug                output debug messages
      -h, --help                 help for rest-server
          --listen string        listen address (default ":8000")
          --log string           log HTTP requests in the combined log format
          --max-size int         the maximum size of the repository in bytes
          --no-auth              disable .htpasswd authentication
          --path string          data directory (default "/tmp/restic")
          --private-repos        users can only access their private repo
          --prometheus           enable Prometheus metrics
          --prometheus-no-auth   disable auth for Prometheus /metrics endpoint
          --tls                  turn on TLS support
          --tls-cert string      TLS certificate path
          --tls-key string       TLS key path
      -V, --version              output version and exit

By default the server persists backup data in `/tmp/restic`.  To start the server with a custom persistence directory and with authentication disabled:

    rest-server --path /user/home/backup --no-auth

To authenticate users (for access to the rest-server), the server supports using a `.htpasswd` file to specify users. You can create such a file at the root of the persistence directory by executing the following command (note that you need the `htpasswd` program from Apache's http-tools).  In order to append new user to the file, just omit the `-c` argument.  Only bcrypt and SHA encryption methods are supported, so use -B (very secure) or -s (insecure by today's standards) when adding/changing passwords.

    htpasswd -B -c .htpasswd username

If you want to disable authentication, you must add the `--no-auth` flag. If this flag is not specified and the `.htpasswd` cannot be opened, rest-server will refuse to start.

NOTE: In older versions of rest-server (up to 0.9.7), this flag does not exist and the server disables authentication if `.htpasswd` is missing or cannot be opened.

By default the server uses HTTP protocol.  This is not very secure since with Basic Authentication, user name and passwords will be sent in clear text in every request.  In order to enable TLS support just add the `--tls` argument and add a private and public key at the root of your persistence directory. You may also specify private and public keys by `--tls-cert` and `--tls-key`.

Signed certificate is required by the restic backend, but if you just want to test the feature you can generate unsigned keys with the following commands:

    openssl genrsa -out private_key 2048
    openssl req -new -x509 -key private_key -out public_key -days 365

The `--append-only` mode allows creation of new backups but prevents deletion and modification of existing backups. This can be useful when backing up systems that have a potential of being hacked.

To prevent your users from accessing each others' repositories, you may use the `--private-repos` flag which grants access only when a subdirectory with the same name as the user is specified in the repository URL. For example, user "foo" using the repository URLs `rest:https://foo:pass@host:8000/foo` or `rest:https://foo:pass@host:8000/foo/` would be granted access, but the same user using repository URLs `rest:https://foo:pass@host:8000/` or `rest:https://foo:pass@host:8000/foobar/` would be denied access.

Rest Server uses exactly the same directory structure as local backend, so you should be able to access it both locally and via HTTP, even simultaneously.

### Systemd

There's an example [systemd service file](https://github.com/restic/rest-server/blob/master/examples/systemd/rest-server.service) included with the source, so you can get Rest Server up & running as a proper Systemd service in no time.  Before installing, adapt paths and options to your environment.

### Docker

By default, image uses authentication.  To turn it off, set environment variable `DISABLE_AUTHENTICATION` to any value.

Persistent data volume is located to `/data`.

#### Start server

    docker run -p 8000:8000 -v /my/data:/data --name rest_server restic/rest-server

It's suggested to set a container name to more easily manage users (see next section).

You can set environment variable `OPTIONS` to any extra flags you'd like to pass to rest-server.

#### Manage users

##### Add user

    docker exec -it rest_server create_user myuser

or

    docker exec -it rest_server create_user myuser mypassword

##### Delete user

    docker exec -it rest_server delete_user myuser


## Prometheus support and Grafana dashboard

The server can be started with `--prometheus` to expose [Prometheus](https://prometheus.io/) metrics at `/metrics`. If authenticaiton is enabled, this endpoint requires authentication for the 'metrics' user, but this can be overridden with the `--prometheus-no-auth` flag.

This repository contains an example full stack Docker Compose setup with a Grafana dashboard in [examples/compose-with-grafana/](examples/compose-with-grafana/).


## Why use Rest Server?

Compared to the SFTP backend, the REST backend has better performance, especially so if you can skip additional crypto overhead by using plain HTTP transport (restic already properly encrypts all data it sends, so using HTTPS is mostly about authentication).

But, even if you use HTTPS transport, the REST protocol should be faster and more scalable, due to some inefficiencies of the SFTP protocol (everything needs to be transferred in chunks of 32 KiB at most, each packet needs to be acknowledged by the server).

One important safety feature that Rest Server adds is the optional ability to run in append-only mode. This prevents an attacker from wiping your server backups when access is gained to the server being backed up.

Finally, the Rest Server implementation is really simple and as such could be used on the low-end devices, no problem.  Also, in some cases, for example behind corporate firewalls, HTTP/S might be the only protocol allowed.  Here too REST backend might be the perfect option for your backup needs.

## Contributors

Contributors are welcome, just open a new issue / pull request.
