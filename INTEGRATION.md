# Samos Exchange Integration

A Samos node offers multiple interfaces:

* REST API on port 8640
* [JSON-RPC 2.0 API on port 8081](https://github.com/samoslab/samos-jsonrpc/blob/master/README.md) **another service**

The API interfaces do not support authentication or encryption so they should only be used over localhost.

If your application is written in Go, you can use these client libraries to interface with the node:

* [Samos REST API Client Godoc](https://godoc.org/github.com/samoslab/samos/src/gui#Client)

The wallet APIs in the REST API operate on wallets loaded from and saved to `~/.samos/wallets`.

The Samos node's wallet APIs can be enabled with `-enable-wallet-api`.

For a node used to support another application,
it is recommended to use the REST API for blockchain queries and disable the wallet APIs,
and to use the CLI tool for wallet operations (seed and address generation, transaction signing).

<!-- MarkdownTOC autolink="true" bracket="round" -->

- [API Documentation](#api-documentation)
    - [Wallet REST API](#wallet-rest-api)
    - [Samos REST API Client Documentation](#samos-rest-api-client-documentation)
    - [Samos Go Library Documentation](#samos-go-library-documentation)
    - [libsamos Documentation](#libsamos-documentation)
- [Implementation guidelines](#implementation-guidelines)
    - [Scanning deposits](#scanning-deposits)
        - [Using the REST API](#using-the-rest-api)
        - [Using samos as a library in a Go application](#using-samos-as-a-library-in-a-go-application)
    - [Sending coins](#sending-coins)
        - [General principles](#general-principles)
        - [Using the REST API](#using-the-rest-api-1)
        - [Using samos as a library in a Go application](#using-samos-as-a-library-in-a-go-application-1)
        - [Coinhours](#coinhours)
    - [Verifying addresses](#verifying-addresses)
        - [Using the CLI](#using-the-cli-2)
        - [Using the REST API](#using-the-rest-api-2)
        - [Using samos as a library in a Go application](#using-samos-as-a-library-in-a-go-application-2)
        - [Using samos as a library in other applications](#using-samos-as-a-library-in-other-applications)
    - [Checking Samos node connections](#checking-samos-node-connections)
        - [Using the REST API](#using-the-rest-api-3)
        - [Using samos as a library in a Go application](#using-samos-as-a-library-in-a-go-application-3)
    - [Checking Samos node status](#checking-samos-node-status)
        - [Using the REST API](#using-the-rest-api-4)
        - [Using samos as a library in a Go application](#using-samos-as-a-library-in-a-go-application-4)

<!-- /MarkdownTOC -->


## API Documentation

### Wallet REST API

[Wallet REST API](src/gui/README.md).

### Samos REST API Client Documentation

[Samos REST API Client](https://godoc.org/github.com/samoslab/samos/src/gui#Client)

### Samos Go Library Documentation

[Samos Godoc](https://godoc.org/github.com/samoslab/samos)

### libsamos Documentation

[libsamos documentation](/lib/cgo/README.md)

## Implementation guidelines

### Scanning deposits

There are multiple approaches to scanning for deposits, depending on your implementation.

One option is to watch for incoming blocks and check them for deposits made to a list of known deposit addresses.
Another option is to check the unspent outputs for a list of known deposit addresses.

#### Using the REST API

To scan the blockchain, call `GET /last_blocks?num=` or `GET /blocks?start=&end=`. There will return block data as JSON
and new unspent outputs sent to an address can be detected.

To check address outputs, call `GET /outputs?addrs=`. If you only want the balance, you can call `GET /balance?addrs=`.

* [`/last_blocks` docs](src/gui/README.md#get-last-n-blocks)
* [`/blocks` docs](src/gui/README.md#get-blocks-in-specific-range)
* [`/outputs` docs](src/gui/README.md#get-unspent-output-set-of-address-or-hash)
* [`/balance` docs](src/gui/README.md#get-balance-of-addresses)

#### Using samos as a library in a Go application

We recommend using the [Samos REST API Client](https://godoc.org/github.com/samoslab/samos/src/gui#Client).

### Sending coins

#### General principles

After each spend, wait for the transaction to confirm before trying to spend again.

For higher throughput, combine multiple spends into one transaction.

Samos uses "coin hours" to ratelimit transactions.
The total number of coinhours in a transaction's outputs must be 50% or less than the number of coinhours in a transaction's inputs,
or else the transaction is invalid and will not be accepted. A transaction must have at least 1 input with at least 1 coin hour.
Sending too many transactions in quick succession will use up all available coinhours.
Coinhours are earned at a rate of 1 coinhour per coin per hour, calculated per second.
This means that 3600 coins will earn 1 coinhour per second.
However, coinhours are only updated when a new block is published to the blockchain.
New blocks are published every 10 seconds, but only if there are pending transactions in the network.

To avoid running out of coinhours in situations where the application may frequently send,
the sender should batch sends into a single transaction and send them on a
30 second to 1 minute interval.

There are other strategies to minimize the likelihood of running out of coinhours, such
as splitting up balances into many unspent outputs and having a large balance which generates
coinhours quickly.

#### Using the REST API

When sending coins via the REST API, a wallet file local to the samos node is used.
The wallet file is specified by wallet ID, and all wallet files are in the
configured data directory (which is `$HOME/.samos/wallets` by default).

#### Using samos as a library in a Go application

A REST API client is also available: [Samos REST API Client Godoc](https://godoc.org/github.com/samoslab/samos/src/gui#Client).

#### Coinhours
Transaction fees in samos is paid in coinhours and is currently set to `50%`,
every transaction created burns `50%` of the total coinhours in all the input
unspents.

You need a minimum of `1` of coinhour to create a transaction.

Coinhours are generated at a rate of `1 coinsecond` per `second`
which are then converted to `coinhours`, `1` coinhour = `3600` coinseconds.

> Note: Coinhours don't have decimals and only show up in whole numbers.

##### REST API
When using the `REST API` the coinhours are distributed as follows:
- 50% of the total coinhours in the unspents being used are burned.
- 25% of the coinhours go to the the change address along with the remaining coins
- 25% of the coinhours are split equally between the receivers

For e.g, If an address has `10` samoss and `50` coinhours and only `1` unspent.
If we send `5` samoss to another address then that address will receive
`5` samoss and `12` coinhours, `26` coinhours(burned coinhours are made even) will be burned.
The sending address will be left with `5` samoss and `12` coinhours which
will then be sent to the change address.

### Verifying addresses

#### Using the CLI

```sh
samos-cli verifyAddress $addr
```

#### Using the REST API

Not directly supported, but API calls that have an address argument will return `400 Bad Request` if they receive an invalid address.

#### Using samos as a library in a Go application

https://godoc.org/github.com/samoslab/samos/src/cipher#DecodeBase58Address

```go
if _, err := cipher.DecodeBase58Address(address); err != nil {
    fmt.Println("Invalid address:", err)
    return
}
```

#### Using samos as a library in other applications

Address validation is available through a C wrapper, `libsamos`.

See the [libsamos documentation](/lib/cgo/README.md) for usage instructions.

### Checking Samos node connections

#### Using the REST API

* `/network/connections`

#### Using samos as a library in a Go application

Use the [Samos REST API Client](https://godoc.org/github.com/samoslab/samos/src/gui#Client)

### Checking Samos node status

#### Using the REST API

A method similar to `samos-cli status` is not implemented, but these endpoints can be used:

* `/version`
* `/blockchain/metadata`
* `/blockchain/progress`
