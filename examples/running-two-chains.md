# Running two chains

This example shows how to run two chains on the same machine.

First, we need to deploy the first chain:

```bash
$ go run main.go cook opstack
```

This chain is going to run under the default `ethplayground` Docker network. Playground uses DNS resolution to discover services in the same network.

In order to run a second chain, we can use the same command and specify a different network name:

```bash
$ go run main.go cook opstack --network eth2
```

This will deploy the second chain under the `eth2` Docker network.
