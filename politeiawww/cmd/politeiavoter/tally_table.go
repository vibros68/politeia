package main

import (
	pb "decred.org/dcrwallet/rpc/walletrpc"
	"fmt"
	tkv1 "github.com/decred/politeia/politeiawww/api/ticketvote/v1"
	"math"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

type ReqVoteConfig struct {
	YesRate float64
	NoRate  float64
	YesVote int
	NoVote  int
	me      *VoteStats
	them    *VoteStats
}

func (r *ReqVoteConfig) TargetApproval() float64 {
	return r.YesRate / r.Participation()
}

func (r *ReqVoteConfig) Total() int {
	return r.YesVote + r.NoVote
}

func (r *ReqVoteConfig) Participation() float64 {
	return r.YesRate + r.NoRate
}

func (p *piv) tallyTable(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("tally-table: not enough arguments %v", args)
	}
	var token = args[0]
	fmt.Printf("Getting stats table... \n")
	v, err := p.getVersion()
	if err != nil {
		return err
	}
	// Get vote details.
	dr, err := p.voteDetails(token, v.PubKey)
	if err != nil {
		return err
	}
	// Find eligble tickets
	tix, err := convertTicketHashes(dr.Vote.EligibleTickets)
	if err != nil {
		return err
	}
	rr, err := p.voteResults(token, v.PubKey)
	if err != nil {
		return err
	}
	ctres, err := p.wallet.CommittedTickets(p.ctx,
		&pb.CommittedTicketsRequest{
			Tickets: tix,
		})
	if err != nil {
		return err
	}
	votedYes, votedNo, eligible, err := p.eligibleVotes(rr, ctres)
	if err != nil {
		return err
	}
	grouping, err := p.proposalGrouping(dr, eligible, votedYes, votedNo)
	if err != nil {
		return err
	}
	err = p.printTallyTable(dr, grouping)
	if err != nil {
		return err
	}
	rvc, err := p.getConfigVoter(args, rr, ctres)
	if err != nil {
		return err
	}
	fmt.Printf("- config yes %.2f%% no %.2f%% (participation %.2f%%, target approval %.2f%%)\n",
		rvc.YesRate*100, rvc.NoRate*100, rvc.Participation()*100, rvc.TargetApproval()*100)
	voteConf := defaultVoter
	voteConf.Participation = rvc.Participation()
	_, _, err = p.getNeededVotes(&voteConf, dr, grouping)
	return err
}

func (p *piv) getConfigVoter(args []string, rr *tkv1.ResultsReply, ctres *pb.CommittedTicketsResponse) (*ReqVoteConfig, error) {
	if len(args) == 6 {
		if args[2] != voteOptionYes {
			return nil, fmt.Errorf("invalid argument, see the example to correct it")
		}
		if args[4] != voteOptionNo {
			return nil, fmt.Errorf("invalid argument, see the example to correct it")
		}
	} else if len(args) == 4 {
		if args[2] != voteOptionMirror {
			return nil, fmt.Errorf("invalid argument, see the example to correct it")
		}
	} else {
		return nil, fmt.Errorf("invalid argument, see the example to correct it")
	}
	me, them, err := p.statsVotes(rr, ctres)
	if err != nil {
		return nil, err
	}
	var mode = args[1]
	var rvc = &ReqVoteConfig{
		me:   me,
		them: them,
	}
	switch mode {
	case voteModeNumber:
		if len(args) == 6 {
			rvc.YesVote, _ = strconv.Atoi(args[3])
			rvc.NoVote, _ = strconv.Atoi(args[5])
			rvc.YesRate = float64(rvc.YesVote) / float64(me.Total())
			rvc.NoRate = float64(rvc.NoVote) / float64(me.Total())
		}
		if len(args) == 4 {
			p.cfg.isMirror = true
			total, _ := strconv.Atoi(args[3])
			rvc.YesVote = int(float64(total) * them.Rate())
			rvc.NoVote = total - rvc.YesVote
			rvc.YesRate = float64(rvc.YesVote) / float64(me.Total())
			rvc.NoRate = float64(rvc.NoVote) / float64(me.Total())
		}
		if rvc.Total() > me.Total() {
			return nil, fmt.Errorf("entered amount(%d) is greater than your total own votes: %d", rvc.Total(), me.Total())
		}

	case voteModePercent:
		if len(args) == 6 {
			rvc.YesRate, _ = strconv.ParseFloat(args[3], 64)
			rvc.NoRate, _ = strconv.ParseFloat(args[5], 64)
		}
		if len(args) == 4 {
			p.cfg.isMirror = true
			totalRate, _ := strconv.ParseFloat(args[3], 64)
			rvc.YesRate = totalRate * them.Rate()
			rvc.NoRate = totalRate - rvc.YesRate
		}
		if rvc.YesRate < 0 || rvc.NoRate < 0 {
			return nil, fmt.Errorf("rate must be > 0 and < 1")
		}
		if rvc.Participation() > 1 {
			return nil, fmt.Errorf("total rate yes and rate no is greater than 1")
		}
		roughYes := float64(me.Total()) * rvc.YesRate
		roughNo := float64(me.Total()) * rvc.NoRate
		rvc.YesVote = int(math.Round(roughYes))
		rvc.NoVote = int(math.Round(roughNo))
	default:
		return nil, fmt.Errorf("mode [%s] is not supported", mode)
	}
	return rvc, nil
}

func (p *piv) getNeededVotes(proposalConfig *VoterConfig, proposal *tkv1.DetailsReply, vig *VotesInfoGroup) (int32, int32, error) {
	targetParticipation := participationTarget(proposalConfig.Participation, proposalConfig.ParticipationMode, vig)

	if targetParticipation == 0 {
		return 0, 0, fmt.Errorf("participation set to 0.. voting disabled")
	}

	if proposalConfig.Participation*float64(vig.Me.Pool) < 1 {
		return 0, 0, fmt.Errorf("participation rate is too low, target: %f, pool: %d, votes: %f",
			targetParticipation, vig.Me.Pool, targetParticipation*float64(vig.Me.Pool))
	}

	bestBlock, err := p.GetBestBlock()
	if err != nil {
		return 0, 0, err
	}

	predictedParticipationMe, err := getPredictedParticipation(float64(vig.Me.All())/float64(vig.Me.Pool),
		proposal.Vote.StartBlockHeight, proposal.Vote.EndBlockHeight, bestBlock)
	if err != nil {
		return 0, 0, err
	}

	predictedParticipationMeRound := math.Round(predictedParticipationMe*10000) / 10000
	targetApproval := proposalConfig.EvaluateTargetApproval(vig)
	if vig.Me.ParticipationRate() >= targetParticipation {
		fmt.Printf("- target participation %.2f%%, current participation %.2f%%, predicted participation %.2f%%\n",
			targetParticipation*100, vig.Me.ParticipationRate()*100, predictedParticipationMeRound*100)
		fmt.Printf("- target approval %v%%, current approval %v%%\n", targetApproval*100, vig.Total().ApprovalRate()*100)
		return 0, 0, fmt.Errorf("participation target has been reached")
	}
	neededYesVotes, neededNoVotes := proposalConfig.CalculateNeededVotes(targetParticipation, vig)
	neededYesVotes = math.Round(neededYesVotes)
	neededNoVotes = math.Round(neededNoVotes)

	return int32(neededYesVotes), int32(neededNoVotes), nil
}

func (p *piv) proposalGrouping(details *tkv1.DetailsReply, eligibleTickets, votedYes, votedNo []*pb.CommittedTicketsResponse_TicketAddress) (*VotesInfoGroup, error) {

	votesResultsReply, err := p.fetchVoteResults(details.Vote.Params.Token)
	if err != nil {
		return nil, err
	}

	return group(eligibleTickets, votedYes, votedNo, details, votesResultsReply)
}

func group(eligibleTickets, votedYes, votedNo []*pb.CommittedTicketsResponse_TicketAddress, detailsReply *tkv1.DetailsReply, voteResults *tkv1.ResultsReply) (*VotesInfoGroup, error) {
	var me, public, total VotesInfo
	me = VotesInfo{
		Yes:  uint(len(votedYes)),
		No:   uint(len(votedNo)),
		Pool: len(eligibleTickets) + len(votedYes) + len(votedNo),
	}
	pool := len(detailsReply.Vote.EligibleTickets)
	// group cast votes into Yes and No votes
	count := make(map[uint64]TicketsCounting)
	for _, v := range voteResults.Votes {
		bits, err := strconv.ParseUint(v.VoteBit, 10, 64)
		if err != nil {
			continue
		}

		partCounting := count[bits]
		partCounting.Count++
		partCounting.Tickets = append(partCounting.Tickets, v.Ticket)
		count[bits] = partCounting
	}

	for _, vo := range detailsReply.Vote.Params.Options {
		part := count[vo.Bit]

		if vo.ID == VoteIdYes {
			total.Yes = part.Count
			public.Yes = total.Yes - me.Yes
		} else if vo.ID == VoteIdNo {
			total.No = part.Count
			public.No = total.No - me.No
		}
	}

	total.Pool = pool
	public.Pool = pool - me.Pool

	grouping := &VotesInfoGroup{
		Me:     me,
		Public: public,
	}
	return grouping, nil
}

func (p *piv) printTallyTable(proposal *tkv1.DetailsReply, grouping *VotesInfoGroup) error {
	bestBlock, err := p.GetBestBlock()
	if err != nil {
		return err
	}

	title := proposal.Vote.Params.Token
	blockInfo, err := blockInfoSummary(proposal.Vote, bestBlock)
	if err != nil {
		return err
	}

	fmt.Printf("[%s] %v, %v \n", viewTime(time.Now()), title, blockInfo)
	return _printTallyTable(grouping, proposal.Vote, bestBlock)
}

// blockInfoSummary returns the number of blocks remaining and percentage of voting window
// completed the proposal.
func blockInfoSummary(voteDetails *tkv1.VoteDetails, latestBlock int32) (string, error) {
	remainingBlocks := int32(voteDetails.EndBlockHeight) - latestBlock
	timeLeftInVote := time.Duration(remainingBlocks) * activeNetParams.TargetTimePerBlock
	percentageTimeComplete := getTimePercentageComplete(voteDetails.StartBlockHeight, voteDetails.EndBlockHeight, latestBlock)
	remainingVotingWindow := fmt.Sprintf("%.2f%%", percentageTimeComplete)
	estEndtime := time.Now().Add(timeLeftInVote)
	return fmt.Sprintf("#%d, %d blocks remaining (%s at %s), %s done",
		latestBlock, remainingBlocks, timeLeftInVote, viewTime(estEndtime), remainingVotingWindow), nil
}

func _printTallyTable(vig *VotesInfoGroup, voteDetails *tkv1.VoteDetails, currentBlockHeight int32) error {
	defer fmt.Println()
	percentDecimal := func(all uint, pool int) float64 {
		if pool == 0 {
			return 0
		}
		return float64(all) / float64(pool)
	}

	me := vig.Me
	public := vig.Public
	totalVotesInfo := vig.Total()

	mePart := percentDecimal(vig.Me.All(), vig.Me.Pool)
	themPart := percentDecimal(vig.Public.All(), vig.Public.Pool)
	totalPart := percentDecimal(vig.Total().All(), vig.Total().Pool)

	predictedParticipationMe, err := getPredictedParticipation(mePart, voteDetails.StartBlockHeight, voteDetails.EndBlockHeight, currentBlockHeight)
	if err != nil {
		return err
	}

	predictedParticipationThem, err := getPredictedParticipation(themPart, voteDetails.StartBlockHeight, voteDetails.EndBlockHeight, currentBlockHeight)
	if err != nil {
		return err
	}

	predictedParticipationTotal, err := getPredictedParticipation(totalPart, voteDetails.StartBlockHeight, voteDetails.EndBlockHeight, currentBlockHeight)
	if err != nil {
		return err
	}

	var predictedVoteMe, predictedVoteThem, predictedVoteTotal float64

	if mePart > 0 {
		predictedVoteMe = math.Round(float64(me.All()) * (predictedParticipationMe / mePart))
	}

	if themPart > 0 {
		predictedVoteThem = math.Round(float64(public.All()) * (predictedParticipationThem / themPart))
	}

	if totalPart > 0 {
		predictedVoteTotal = math.Round(float64(totalVotesInfo.All()) * (predictedParticipationTotal / totalPart))
	}

	predictedParticipationMeString := fmt.Sprintf("%.4f%%", predictedParticipationMe*100)
	predictedParticipationThemString := fmt.Sprintf("%.4f%%", predictedParticipationThem*100)
	predictedParticipationTotalString := fmt.Sprintf("%.4f%%", predictedParticipationTotal*100)

	totalRemaining := totalVotesInfo.remainingVotes()
	totalParticipation := fmt.Sprintf("%.4f%%", vig.Total().ParticipationRate()*100)
	totalApproval := vig.Total().ApprovalPercentage()

	meRemaining := me.remainingVotes()
	meParticipation := vig.Me.ParticipationPercentage()
	meApproval := vig.Me.ApprovalPercentage()

	themRemaining := public.remainingVotes()
	themParticipation := vig.Public.ParticipationPercentage()
	themApproval := vig.Public.ApprovalPercentage()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.AlignRight|tabwriter.Debug)
	fmt.Fprintln(w, strings.Join([]string{
		"",
		"total",
		"remaining",
		"voted",
		"participation",
		"yes",
		"no",
		"approval",
		"predicted",
		"end",
	}, "\t"))
	fmt.Fprintf(w, "all \t%v \t%v \t%v \t%v \t%v \t%v \t%v \t%v \t %v \n", totalVotesInfo.Pool, totalRemaining,
		totalVotesInfo.All(), totalParticipation, totalVotesInfo.Yes, totalVotesInfo.No, totalApproval, predictedParticipationTotalString, predictedVoteTotal)
	fmt.Fprintf(w, "them \t %v \t %v \t %v \t %v \t %v \t %v \t %v \t %v \t %v \n", public.Pool, themRemaining, public.All(),
		themParticipation, public.Yes, public.No, themApproval, predictedParticipationThemString, predictedVoteThem)
	fmt.Fprintf(w, "me \t %v \t %v \t %v \t %v \t %v \t %v \t %v \t %v \t %v \n", me.Pool, meRemaining, me.All(), meParticipation,
		me.Yes, me.No, meApproval, predictedParticipationMeString, predictedVoteMe)
	return w.Flush()
}

// getPredictedParticipation returns the predicted participation rate based on the completed voting window
func getPredictedParticipation(participation float64, startHeight,
	endHeight uint32, currentBlockHeight int32) (predictedParticipation float64, err error) {
	percentageTimeComplete := getTimePercentageComplete(startHeight, endHeight, currentBlockHeight)
	participationMultiplier := 1 / (percentageTimeComplete / 100)

	predictedParticipation = math.Round((participation*float64(participationMultiplier))*10000) / 10000
	if predictedParticipation > 1 {
		//fmt.Printf("\t- PredictedParticipation: %.4f%%, setting to 100%%\n", predictedParticipation*100)
		predictedParticipation = 1
	}
	return
}

// getTimePercentageComplete returns the completed percentage of a proposal
func getTimePercentageComplete(startBlock uint32, endBlock uint32, currentBlock int32) float32 {
	var completedBlocks, totalBlocks float32
	totalBlocks = float32(endBlock) - float32(startBlock)
	completedBlocks = float32(uint32(currentBlock) - startBlock)
	if completedBlocks >= totalBlocks {
		completedBlocks = totalBlocks
	}
	return (completedBlocks / totalBlocks) * 100
}

func participationTarget(participation float64, participationMode string, vig *VotesInfoGroup) float64 {
	mePool, meAll := float64(vig.Me.Pool), float64(vig.Me.All())
	neededParticipation := func(leftToVote float64, targetTickets int) float64 {
		if leftToVote < 1 {
			fmt.Printf("Target tickets: %d, Left to vote: %d, neededMe: 0.000%%, Comment: No more votes are needed to reach target.", targetTickets,
				int(leftToVote))
			return 0
		}

		if leftToVote > (mePool - meAll) {
			fmt.Printf("Target tickets: %d, Left to vote: %d, neededMe: 100.000%%, Comment: Available tickets are unable to reach target, using all available tickets.", targetTickets,
				int(leftToVote))
			return 1
		}

		part := (leftToVote + meAll) / mePool
		fmt.Printf("Target tickets: %d, Left to vote: %d, neededMe: %.3f%%", targetTickets, int(leftToVote), part*100)
		return part
	}

	switch participationMode {
	case ModeAll:
		allTickets := float64(vig.Total().Pool)
		targetTickets := participation * allTickets
		totalAll := float64(vig.Total().All())
		leftToVote := targetTickets - totalAll
		return neededParticipation(leftToVote, int(targetTickets))
	case ModeThem:
		themAll := float64(vig.Public.All())
		targetTickets := participation * themAll
		leftToVote := targetTickets - meAll
		return neededParticipation(leftToVote, int(targetTickets))
	default:
		return participation
	}
}
