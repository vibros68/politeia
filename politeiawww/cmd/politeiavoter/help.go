// Copyright (c) 2021 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

const listCmdMessage = `Available commands:
  inventory   Retrieve all proposals that are being voted on
  vote        Vote on a proposal
  tally       Tally votes on a proposal
  tally-table Tally votes in table
  verify      Verify votes on a proposal
  stats       Print stats information about proposals
  help        Print detailed help message for a command`

const inventoryHelpMsg = `inventory 

Retrieve all proposals that are being voted on.`

const voteHelpMsg = `vote [tokenId] yes [yesRate] no [noRate]

Vote on a proposal.

Arguments:
1. tokenId   (string, required)  Proposal censorship token id
2. yes (string, required)  Vote option ID yes
3. yesRate (float, required) 0 <= yesRate <= 1
4. no (string, required)  Vote option ID yes
5. noRate (float, required) 0 <= noRate <= 1`

const tallyHelpMsg = `tally "token"

Tally votes on a proposal.

Arguments:
1. token   (string, required)  Proposal censorship token`

const tallyTableHelpMsg = `tally-table "token"

Tally votes in a table on a proposal.

Arguments:
1. token   (string, required)  Proposal censorship token`

const verifyHelpMsg = `verify "tokens..."

Verify votes on proposals. If no tokens are provided or 'ALL' string is 
provided then it verifies all votes present in the vote dir.

Arguments:
1. tokens  ([]string, optional)  Proposal tokens.`
