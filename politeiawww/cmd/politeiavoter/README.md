# Politeia Voter

`politeiavoter` is a command line utility that can be used to issue votes on
proposals.

Configuration and logs Linux/BSD/POSIX:
The tool keeps logs and configuration files in the ~/.politeiavoter directory

Configuration and logs Windows:
The tool keeps logs and configuration files in the %LOCALAPPDATA%\Politeiavoter
directory

Configuration and logs macOS/OSX:
The tool keeps logs and configuration files in the ~/Library/Application
Support/Politeiavoter directory

In the following examples the config file contained the following entry:
```
testnet=1
```

If you want to run multiple pivoters on the same machine while keeping them
physically separated use the `appdata` setting in the config file. Then launch
`politeiavoter` and point it to the config file using the `-C` option.  For
example:
```
$ politeiavoter -C ~/.politeiavoter2/politeiavoter.conf
```
Excerpt from the `politeiavoter.conf` file:
```
appdata=~/.politeiavoter2
```

## Requirements

Voting requires access to wallet GRPC. Therefore this tool needs the wallet's
server certificate to authenticate the server, as well as a local client keypair
to authenticate the client to `dcrwallet`.  The server certificate by default
will be found in `~/.dcrwallet/rpc.cert`, and this can be modified to another
path using the `--walletgrpccert` flag.  Client certs can be generated using
[`gencerts`](https://github.com/decred/dcrd/blob/master/cmd/gencerts/) and
`politeiavoter` will read `client.pem` and `client-key.pem` from its application
directory by default.  The certificate (`client.pem`) must be appended to
`~/.dcrwallet/clients.pem` in order for `dcrwallet` to trust the client.

For example:

```
$ gencerts ~/.politeiavoter/client{,-key}.pem
$ cat ~/.politeiavoter/client.pem >> ~/.dcrwallet/clients.pem
```

In order to sign votes ```politeiavoter``` requires the wallet passphrase.

In order to use the "vote trickler" functionality one must use Tor. Without Tor
the server administrator will still know where the votes came from rendering
the trickling worthless.

## Workflow

```politeiavoter``` supports four commands:

```
  inventory - Retrieve all proposals that are being voted on
  vote      - Vote on a proposal
  tally     - Tally votes on a proposal
  verify    - Verify a or ALL votes
```

First one obtains the list of active proposals that are up for voting:
```
politeiavoter inventory
```

This will output all eligible votes.
```
Vote: c180fc047e38e455
  Proposal        : This is a description
  Start block     : 282899
  End block       : 284915
  Mask            : 3
  Eligible tickets: 9
  Vote Option:
    Id                   : no
    Description          : Don't approve proposal
    Bits                 : 1
    To choose this option: politeiavoter vote c180fc047e38e455 no
  Vote Option:
    Id                   : yes
    Description          : Approve proposal
    Bits                 : 2
    To choose this option: politeiavoter vote c180fc047e38e455 yes
```

In this example the user has **9** eligible tickets to vote.

The vote choice is printed during inventory and one can simply copy & paste
that into the shell.

```
politeiavoter vote c180fc047e38e455 yes
```
The tool will prompt for the wallet decryption passphrase and then takes a few
seconds to vote.

```
Enter the private passphrase of your wallet:
Votes succeeded: 9
Votes failed   : 0
```

Note: that the tool at this time votes the same choice for **all available**
tickets.

To get the current tally of votes.
```
politeiavoter tally 8bdebbc55ae74066cc57c76bc574fd1517111e56b3d1295bde5ba3b0bd7c3f67
Vote Option:
  Id                   : no
  Description          : Don't approve proposal
  Bits                 : 1
  Votes received       : 0
  Percentage           : 0%
Vote Option:
  Id                   : yes
  Description          : Approve proposal
  Bits                 : 2
  Votes received       : 9
  Percentage           : 100%
```

## Cross verification of vote data

The `verify` command verifies the local journals against the `politeia` recoded
voting activity. The point of this command is to enable the human to determine
if vote failures occured and provides the necessary data to debug issues. For a
non-developer this option is only interesting to see if the journals match the
server data. The verify action can only be run on a completed vote.

Display all votes that have occured:
```
$ politeiavoter verify
Votes:
  012b4e335f25704e28ef196d650316dca421f730225d39e37b31b3c646eb8497
  023091831f6434f743f3a317aacf8c73a123b30d758db854a2f294c0b3341bcc
```

Verify a single vote:
```
$ politeiavoter verify 023091831f6434f743f3a317aacf8c73a123b30d758db854a2f294c0b3341bcc
== NO failed votes proposal 023091831f6434f743f3a317aacf8c73a123b30d758db854a2f294c0b3341bcc
```

Verify all votes:
```
$ politeiavoter verify ALL
== NO failed votes proposal 012b4e335f25704e28ef196d650316dca421f730225d39e37b31b3c646eb8497
== NO failed votes proposal 023091831f6434f743f3a317aacf8c73a123b30d758db854a2f294c0b3341bcc
```

## Privacy considerations

By default, ```politeiavoter``` votes all eligible tickets in a single shot.
Thus giving away to the server operator which IP address controls which
tickets.  While this information is NOT visible externally the more privacy
conscience user may want to spread voting out over time and using tor to mask
IP address.

```politeiavoter``` has three settings to control this behavior. First there is
the ```--trickle``` setting. This must be set to enable trickling. The second
setting is ```--proxy```. This setting makes ```politeiavoter``` use a Tor
proxy and is *REQUIRED* when trickling votes since it makes no sense to trickle
votes from the same IP.

The third setting is ```--voteduration```. This sets the maximum duration to
trickle out votes. Valid modifiers are h for hours, m for minutes and s for
seconds (e.g. 3h18m15s). If this setting is NOT set then ```politeiavoter```
will try to spread the votes out over the remaining vote duration minus one
day.  If it can't autodetect a proper duration it will error out so that the
user can provide one.

E.g. running Tor software on the local machine with 10 votes:
```
politeiavoter --proxy=127.0.0.1:9050 --trickle --voteduration=30m vote 8bdebbc55ae74066cc57c76bc574fd1517111e56b3d1295bde5ba3b0bd7c3f67 yes
```

Running Tor software on the local machine with 10 votes and autodetect duration:
```
politeiavoter --proxy=127.0.0.1:9050 --trickle vote 8bdebbc55ae74066cc57c76bc574fd1517111e56b3d1295bde5ba3b0bd7c3f67 yes
```

## vibros68 changes
* add --resume to calculate vote time from starting proposal vote

Normally politeiavoter vote will start vote at the running time. Use --resume make 
the start vote time will be the start time of the proposal's start time. Each vote time 
is generated randomly. Vote generated in the pass time will be ignored and continue to
generate till the time is in the future
* vote both yes and no in the same time and support vote mode. 
There are 3 mode: **mirror**, **percent** and **number**. 
* **mirror** mode: yes and no vote will be calculated based on other people votes.
we look at what percentage the other 'them' votes are and we mirror it

example: 
```
politeiavoter vote c180fc047e38e455 mirror
```
* **percent** mode: yes and no vote will be calculated based on the percent of 
 input entry. Bellow is an example: 
```
politeiavoter vote c180fc047e38e455 percent yes 0.2 no 0.5
```
* **number** mode: amount of yes/no vote is entered directly.
```
politeiavoter vote c180fc047e38e455 number yes 69 no 80
```
* **mirror** option: mirroring them vote
```
politeiavoter vote c180fc047e38e455 number mirror 200
politeiavoter vote c180fc047e38e455 percent mirror 1.0
```
* implement gaussian distribution for calculating vote time
```
politeiavoter --gaussian vote c180fc047e38e455 percent yes 0.2 no 0.5
```
using `--gaussian` will generate vote time by 
[gaussian distribution](https://en.wikipedia.org/wiki/Normal_distribution)
algorithm. without `--gaussian` using `bunches` mode as the upstream does
* cache request proposal api
* add --emulatevote=[number of assumed vote]

Do fake vote. Purpose for testing

* add --starttimeoffset=53h30m1s (for eg.), used to add duration offset to start vote time
  (affect with --resume)

