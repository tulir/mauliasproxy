# Maunium Matrix room alias proxy
This is a simple room alias proxy that can respond to the [federation alias
query endpoint]. The purpose is to allow creating room addresses with vanity
domains without having to run a full-blown Matrix homeserver.

Discussion room: [#maunium:mau.dev](https://matrix.to/#/#maunium:mau.dev)

[federation alias query endpoint]: https://spec.matrix.org/v1.7/server-server-api/#get_matrixfederationv1querydirectory

## Setup
You can either build the Go program yourself (just `git clone` + `go build`),
or use the docker image `dock.mau.dev/tulir/mauliasproxy`.

After that, copy [example-config.yaml](example-config.yaml) to `config.yaml`
and fill out the details you want.  If using docker, mount the directory with
`config.yaml` at `/data`.

Finally set up your reverse proxy to proxy `/_matrix/federation/v1/query/directory`
on the alias domains to mauliasproxy.

Optionally, you may also proxy:
* `/.well-known/matrix/server` to have mauliasproxy handle delegation to 443.
* `/_matrix/federation/v1/version` ~~and `/_matrix/key/v2/server` to make the federation tester pass~~.
* `/_matrix/federation/*` to respond with a proper `M_NOT_FOUND` code to make old Synapses work.
