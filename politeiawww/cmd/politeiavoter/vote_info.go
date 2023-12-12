package main

import (
	"fmt"
	"math"
	"time"
)

const (
	ModeAll           = "all"
	ModeMe            = "me"
	ModeThem          = "them"
	ModeParticipation = "participation"
)

const (
	ConstantMode = "constant"
	SpreadMode   = "spread"
	BarMode      = "bar"

	DefaultActive                  = true
	DefaultPrintTable              = true
	DefaultPrioritizeParticipation = false
	DefaultParticipation           = 1.0
	DefaultParticipationRate       = 0.5
	DefaultParticipationMode       = ModeMe
	DefaultApprovalMode            = ModeAll
	DefaultApprovalUpper           = 1
	DefaultApprovalLower           = 1
	DefaultConstantSpeedSec        = 0
	DefaultResyncSec               = 3600
	DefaultActiveVotesUpdateSec    = 7200
	DefaultWindow                  = 1
	DefaultVoteRandomness          = 1
	DefaultVoteStatusUpdateSec     = 3600
	DefaultAsyncVoteLimit          = 3
	DefaultBarSize                 = 3600
	DefaultBarSpread               = 1.0
	DefaultBarSampling             = 1.0
	DefaultBarRows                 = 0
	DefaultBarChar                 = 100
	DefaultBarRandomness           = 1.0
	DefaultPriority                = true
	DefaultPriorityThreshold       = 0.1
)

var (
	defaultVoter = VoterConfig{
		Active:                  DefaultActive,
		PrintTable:              DefaultPrintTable,
		Name:                    "default",
		ProposalID:              "default",
		SpeedMode:               ConstantMode,
		BarSize:                 DefaultBarSize,
		YesBarSpread:            DefaultBarSpread,
		NoBarSpread:             DefaultBarSpread,
		YesBarSampling:          DefaultBarSampling,
		NoBarSampling:           DefaultBarSampling,
		BarRows:                 DefaultBarRows,
		BarChar:                 DefaultBarChar,
		YesBarRandomness:        DefaultBarRandomness,
		NoBarRandomness:         DefaultBarRandomness,
		PrioritizeParticipation: DefaultPrioritizeParticipation,
		Participation:           DefaultParticipation,
		ParticipationYes:        DefaultParticipationRate,
		ParticipationNo:         DefaultParticipationRate,
		ParticipationMode:       DefaultParticipationMode,
		ApprovalMode:            DefaultApprovalMode,
		Priority:                DefaultPriority,
		PriorityThreshold:       DefaultPriorityThreshold,
		ApprovalUpper:           DefaultApprovalUpper,
		ApprovalLower:           DefaultApprovalLower,
		ConstantSpeedSec:        DefaultConstantSpeedSec,
		Window:                  DefaultWindow,
		VoteRandomness:          DefaultVoteRandomness,
		ResyncSec:               DefaultResyncSec,
	}
)

type (
	VotesInfo struct {
		Yes  uint
		No   uint
		Pool int
	}

	VotesInfoGroup struct {
		Me     VotesInfo
		Public VotesInfo
	}
)

func (v VotesInfo) PrintTB(name string) (rows []string) {
	approval := "..."
	if v.All() != 0 {
		approval = fmt.Sprintf("%.2f%%", (float64(v.Yes)/float64(v.All()))*100)
	}

	total := v.Pool
	remain := "..."
	if total > 0 && total >= (int(v.All())) {
		vLeft := total - int(v.All())
		remain = fmt.Sprintf("%v/ %v (%.2f%%)", vLeft, total, (float64(vLeft)/float64(total))*100)
	} else {
		fmt.Printf("total: %v, all: %v \n", total, v.All())
	}

	rows = []string{
		name,
		fmt.Sprintf("%v/ %v", v.Yes, v.No),
		approval,
		fmt.Sprintf("%v", v.All()),
		remain,
	}
	return
}

func (v VotesInfo) All() uint {
	return v.No + v.Yes
}

// ApprovalRate returns the ratio of the yes votes to no votes
func (v VotesInfo) ApprovalRate() float64 {
	if v.All() > 0 {
		return math.Round(float64(v.Yes)/float64(v.All())*10000) / 10000
	}
	return 0
}

// ApprovalPercentage returns the percentage of yes votes to no votes
func (v VotesInfo) ApprovalPercentage() string {
	return fmt.Sprintf("%.4f%%", v.ApprovalRate()*100)
}

func (v VotesInfo) ParticipationRate() float64 {
	return math.Round(float64(v.All())/float64(v.Pool)*10000) / 10000
}

func (v VotesInfo) ParticipationPercentage() string {
	return fmt.Sprintf("%.4f%%", v.ParticipationRate()*100)
}

func (v VotesInfoGroup) Total() VotesInfo {
	return VotesInfo{
		Yes:  v.Me.Yes + v.Public.Yes,
		No:   v.Me.No + v.Public.No,
		Pool: v.Me.Pool + v.Public.Pool,
	}
}

type TicketsCounting struct {
	Tickets []string
	Count   uint
}

func (v *VotesInfo) remainingVotes() int {
	return v.Pool - int(v.All())
}

type VoterConfig struct {
	Active                  bool          `json:"active"`
	PrintTable              bool          `json:"print_table"`
	Name                    string        `json:"name"`
	ProposalID              string        `json:"proposal_id"`
	SpeedMode               string        `json:"speed_mode"`
	BarSize                 float64       `json:"bar_size"`
	YesBarSpread            float64       `json:"yes_bar_spread"`
	NoBarSpread             float64       `json:"no_bar_spread"`
	YesBarSampling          float64       `json:"yes_bar_sampling"`
	NoBarSampling           float64       `json:"no_bar_sampling"`
	BarRows                 int           `json:"bar_rows"`
	BarChar                 float64       `json:"bar_char"`
	YesBarRandomness        float64       `json:"yes_bar_randomness"`
	NoBarRandomness         float64       `json:"no_bar_randomness"`
	EmptyYesBars            float64       `json:"empty_yes_bars"`
	EmptyNoBars             float64       `json:"empty_no_bars"`
	Participation           float64       `json:"participation"`
	PrioritizeParticipation bool          `json:"prioritize_participation"`
	ParticipationMode       string        `json:"participation_mode"`
	ParticipationYes        float64       `json:"participation_yes"`
	ParticipationNo         float64       `json:"participation_no"`
	ApprovalMode            string        `json:"approval_mode"`
	ApprovalUpper           float64       `json:"approval_upper"`
	ApprovalLower           float64       `json:"approval_lower"`
	Priority                bool          `json:"priority"`
	PriorityThreshold       float64       `json:"priority_threshold"`
	Filter                  string        `json:"filter"`
	Window                  float64       `json:"window"`
	VoteRandomness          float64       `json:"vote_randomness"`
	ResyncSec               time.Duration `json:"resync_sec"`
	MockVote                int           `json:"mock_vote"`
	ConstantSpeedSec        int           `json:"const_speed_sec"`
}

func (vc *VoterConfig) EvaluateTargetApproval(vig *VotesInfoGroup) (targetApproval float64) {
	var mirroringApproval float64
	public := vig.Public
	if public.All() > 0 {
		mirroringApproval = math.Round(float64(public.Yes)/float64(public.All())*10000) / 10000
	}

	//var boundaryInfo string
	targetApproval, _ = getTargetRateBoundaries(mirroringApproval, vc.ApprovalLower, vc.ApprovalUpper)
	return targetApproval
}

// getTargetRateBoundaries returns the target approval and participation rate based on the lower and
// upper participation and approval safegaurd set by the admin in the configuration file
func getTargetRateBoundaries(target, lower, upper float64) (float64, string) {
	var info string

	if lower == upper {
		info = fmt.Sprintf("using %v", upper)
		return upper, info
	} else if upper > 0 && target > upper {
		info = "using upper"
		return upper, info
	} else if lower > 0 && target < lower {
		info = "using lower"
		return lower, info
	}

	info = "using them"

	return target, info
}

func (vc *VoterConfig) CalculateNeededVotes(participation float64, vig *VotesInfoGroup) (remainingYesVotes, remainingNoVotes float64) {
	targetApproval := vc.EvaluateTargetApproval(vig)

	switch vc.ApprovalMode {
	case ModeAll:
		remainingYesVotes, remainingNoVotes = calculateNeededVotesAll(targetApproval, participation, vc, vig)
	case ModeMe:
		remainingYesVotes, remainingNoVotes = calculateNeededVotesMe(targetApproval, participation, vc, vig)
	case ModeParticipation:
		remainingYesVotes, remainingNoVotes = calculateNeededVotesParticipation(vc, vig)
	}
	return
}

func calculateNeededVotesMe(targetApproval, participation float64, config *VoterConfig, vig *VotesInfoGroup) (neededYesVotes, neededNoVotes float64) {
	config.printParticipationInfo()
	config.printApprovalInfo(vig)
	budgetTickets := participation * float64(vig.Me.Pool)
	return neededVotes(targetApproval, budgetTickets, vig.Me, vig)
}

func neededVotes(targetApproval, budgetTickets float64, targetVotesInfo VotesInfo, vig *VotesInfoGroup) (neededYesVotes, neededNoVotes float64) {
	ticketsLeftParticipation := budgetTickets - float64(vig.Me.All())
	v := targetVotesInfo

	yes, no := float64(v.Yes), float64(v.No)
	highestApproval := (ticketsLeftParticipation + yes) / budgetTickets
	lowestApproval := yes / budgetTickets
	if targetApproval > highestApproval {
		neededYesVotes = ticketsLeftParticipation
		neededNoVotes = 0
		fmt.Printf("\t- Target approval greater than highest approval... assigning all tickets to yes...  needed votes: yes %v  no %v\n", math.Round(ticketsLeftParticipation), 0)
	} else if targetApproval < lowestApproval {
		neededYesVotes = 0
		neededNoVotes = ticketsLeftParticipation
		fmt.Printf("\t- Target approval lesser than lowest approval... assigning all tickets to no...  needed votes: yes %v  no %v\n", 0, math.Round(ticketsLeftParticipation))
	} else {
		targetYesVotes := targetApproval * budgetTickets
		neededYesVotes = targetYesVotes - yes
		neededNoVotes = (budgetTickets - targetYesVotes) - no
	}

	approvalCalculations(vig, budgetTickets, ticketsLeftParticipation)
	fmt.Printf("needed votes: yes: %.f no: %.f total: %.f\n", math.Round(neededYesVotes), math.Round(neededNoVotes),
		math.Round(neededYesVotes)+math.Round(neededNoVotes))
	return
}

func calculateNeededVotesAll(targetApproval, participation float64, config *VoterConfig, vig *VotesInfoGroup) (neededYesVotes, neededNoVotes float64) {
	config.printParticipationInfo()
	config.printApprovalInfo(vig)
	var highestPossibleApprovalSolo, lowestPossibleApprovalSolo, meAll, mePool, totalYes, totalAll float64

	me, total := vig.Me, vig.Total()
	meAll, mePool = float64(me.All()), float64(me.Pool)
	totalYes, totalAll = float64(total.Yes), float64(total.All())

	// derive approvals if all "me" remaining tickets are used
	unusedTickets := mePool - meAll
	highestPossibleApprovalSolo = math.Round(((totalYes+unusedTickets)/(totalAll+unusedTickets))*1000) / 1000
	lowestPossibleApprovalSolo = math.Round(totalYes/(totalAll+unusedTickets)*1000) / 1000

	if targetApproval > highestPossibleApprovalSolo && !config.PrioritizeParticipation {
		fmt.Printf("\t- Approval: ame %v-%v... target approval greater "+
			"than highest approval... assigning all tickets to yes...  needed votes: yes %v  no %v \n", lowestPossibleApprovalSolo,
			highestPossibleApprovalSolo, math.Round(unusedTickets), 0)
		neededYesVotes = unusedTickets
		neededNoVotes = 0
		return
	} else if targetApproval < lowestPossibleApprovalSolo {
		fmt.Printf("\t- Target approval lesser than lowest approval... assigning all tickets to no...  needed votes: yes %v  no %v ", 0, math.Round(unusedTickets))
		neededYesVotes = 0
		neededNoVotes = unusedTickets
		return
	}

	budgetTickets := (participation * mePool) + float64(vig.Public.All())
	neededYesVotes, neededNoVotes = neededVotes(targetApproval, budgetTickets, vig.Total(), vig)
	return
}

// calculateNeededVotesParticipation calculates and returns the needed votes when approval mode is set to "participation"
// and the participation_yes and participation_no configurations are set.
func calculateNeededVotesParticipation(proposalConfig *VoterConfig, vig *VotesInfoGroup) (neededYesVotes, neededNoVotes float64) {
	partYes, partNo := proposalConfig.ParticipationYes, proposalConfig.ParticipationNo
	fmt.Printf("\t- Participation(Config: %.4f%%, Yes %.4f%%, No %.4f%%), Current participation %.4f%%\n",
		(partYes+partNo)*100, partYes*100, partNo*100, (float64(vig.Me.All())/float64(vig.Me.Pool))*100)

	totalNeededYesVotes := partYes * float64(vig.Me.Pool)
	totalNeededNoVotes := partNo * float64(vig.Me.Pool)
	appTarget := totalNeededYesVotes / (totalNeededYesVotes + totalNeededNoVotes)

	currentApproval := 0.0
	if vig.Me.All() > 0 {
		currentApproval = float64(vig.Me.Yes / vig.Me.All())
	}
	fmt.Printf("\t- Approval(mode: %s,  target %.4f%%), Current Approval %.4f%%\n", proposalConfig.ApprovalMode,
		appTarget*100, currentApproval*100)

	allNeededVotes := totalNeededYesVotes + totalNeededNoVotes
	leftToVote := allNeededVotes - float64(vig.Me.All())
	approvalCalculations(vig, allNeededVotes, leftToVote)

	neededYesVotes = math.Round(totalNeededYesVotes - float64(vig.Me.Yes))
	neededNoVotes = math.Round(totalNeededNoVotes - float64(vig.Me.No))
	if neededYesVotes <= 0 {
		neededYesVotes = 0
	}

	if neededNoVotes <= 0 {
		neededNoVotes = 0
	}

	fmt.Printf("\t- Voted: (Yes: %d  No %d),  Needed Votes: (Yes: %.f  No: %.f  Total: %.f)\n", vig.Me.Yes,
		vig.Me.No, neededYesVotes, neededNoVotes, neededYesVotes+neededNoVotes)
	return neededYesVotes, neededNoVotes
}

func (vc *VoterConfig) printParticipationInfo() {
	fmt.Printf("participation %.2f%% \n", vc.Participation*100)
}

func (vc *VoterConfig) printApprovalInfo(vig *VotesInfoGroup) {
	_, boundaryInfo := getTargetRateBoundaries(vig.Public.ApprovalRate(), vc.ApprovalLower, vc.ApprovalUpper)
	minYes, minNo, minPart, total := evaluateMinimumVotes(vig, vc)
	fmt.Printf("approval target: %s them %.4f%% (%v) reach target yes %.f no %.f total %.f part %.4f%%\n", vc.approvalRange(),
		vig.Public.ApprovalRate()*100, boundaryInfo, math.Round(minYes), math.Round(minNo), total, minPart*100)
}

func evaluateMinimumVotes(vig *VotesInfoGroup, config *VoterConfig) (minYes, minNo, minPart, total float64) {
	v := vig.Me
	if config.ApprovalMode == "all" {
		v = vig.Total()
	}

	approval := float64(v.Yes) / float64(v.All())
	targetApproval := EvaluateTargetApproval(config, vig)
	minYes, minNo, minPart = minNeededVotes(approval, targetApproval, vig, config.ApprovalMode)
	total = minYes + minNo + float64(v.All())
	return
}

func minNeededVotes(currentApproval, targetApproval float64, vig *VotesInfoGroup, mode string) (neededYes, neededNo, requiredParticipation float64) {
	rp := func(neededVotes float64) float64 {
		part := (neededVotes + float64(vig.Me.All())) / float64(vig.Me.Pool)
		return math.Round(part*10000) / 10000
	}

	v := vig.Me
	if mode == "all" {
		v = vig.Total()
	}
	yes := float64(v.Yes)
	all := float64(v.All())
	if currentApproval < targetApproval {
		neededYes = ((targetApproval * all) - yes) / (1 - targetApproval)
		requiredParticipation = rp(neededYes)
		return
	} else if currentApproval > targetApproval {
		neededNo = (yes / targetApproval) - all
		requiredParticipation = rp(neededNo)
		return
	}
	return
}

func EvaluateTargetApproval(vc *VoterConfig, vig *VotesInfoGroup) (targetApproval float64) {
	var mirroringApproval float64
	public := vig.Public
	if public.All() > 0 {
		mirroringApproval = math.Round(float64(public.Yes)/float64(public.All())*10000) / 10000
	}

	//var boundaryInfo string
	targetApproval, _ = getTargetRateBoundaries(mirroringApproval, vc.ApprovalLower, vc.ApprovalUpper)
	return targetApproval
}

func approvalCalculations(vig *VotesInfoGroup, budgetTickets, ticketsLeftParticipation float64) {
	total, public, me := vig.Total(), vig.Public, vig.Me

	yes := float64(total.Yes)
	budgetTicketsAll := budgetTickets + float64(public.All())
	lowestApproval, highestApproval := yes/budgetTicketsAll, (ticketsLeftParticipation+yes)/budgetTicketsAll
	pme := fmt.Sprintf("%.4f%%-%.4f%%", toPercent(lowestApproval), toPercent(highestApproval))

	totalAll, unusedTicketsMe := float64(total.All()), float64(me.Pool)-float64(me.All())
	lowestPossibleApprovalSolo := math.Round(yes/(totalAll+unusedTicketsMe)*10000) / 10000
	highestPossibleApprovalSolo := math.Round(((yes+unusedTicketsMe)/(totalAll+unusedTicketsMe))*10000) / 10000
	ame := fmt.Sprintf("%.4f%%-%.4f%%", toPercent(lowestPossibleApprovalSolo), toPercent(highestPossibleApprovalSolo))

	pool, all := float64(total.Pool), float64(total.All())
	unusedTickets := pool - all
	highestPossibleApproval := (unusedTickets + yes) / pool
	lowestPossibleApproval := yes / pool
	aall := fmt.Sprintf("%.4f%%-%.4f%%", toPercent(roundUpFour(lowestPossibleApproval)),
		toPercent(roundUpFour(highestPossibleApproval)))

	fmt.Printf("approval calcs: pme %s  ame %s  aall %s\n", pme, ame, aall)
}

func toPercent(value float64) float64 {
	return value * 100
}

func roundUpFour(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func (vc *VoterConfig) approvalRange() string {
	if vc.ApprovalLower == vc.ApprovalUpper {
		return fmt.Sprintf("%.4f%%", vc.ApprovalLower*100)
	}
	return fmt.Sprintf("%.4f%%-%.4f%%", vc.ApprovalLower*100, vc.ApprovalUpper*100)
}

type VoteStats struct {
	Yes int
	No  int
	Yet int
}

func (v *VoteStats) Total() int {
	return v.Yes + v.No + v.Yet
}

func (v *VoteStats) Rate() float64 {
	if v.Yes == v.No {
		return 0.5
	}
	return float64(v.Yes) / float64(v.Yes+v.No)
}
