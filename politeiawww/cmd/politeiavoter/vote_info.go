package main

import (
	"fmt"
	"math"
)

const (
	ModeAll  = "all"
	ModeMe   = "me"
	ModeThem = "them"
)

const (
	DefaultPrioritizeParticipation = false
	DefaultParticipation           = 1.0
	DefaultParticipationRate       = 0.5
	DefaultParticipationMode       = ModeMe
	DefaultApprovalMode            = ModeAll
	DefaultApprovalUpper           = 1
	DefaultApprovalLower           = 1
)

var (
	defaultVoter = VoterConfig{
		PrioritizeParticipation: DefaultPrioritizeParticipation,
		Participation:           DefaultParticipation,
		ParticipationYes:        DefaultParticipationRate,
		ParticipationNo:         DefaultParticipationRate,
		ParticipationMode:       DefaultParticipationMode,
		ApprovalMode:            DefaultApprovalMode,
		ApprovalUpper:           DefaultApprovalUpper,
		ApprovalLower:           DefaultApprovalLower,
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
	Participation           float64 `json:"participation"`
	PrioritizeParticipation bool    `json:"prioritize_participation"`
	ParticipationMode       string  `json:"participation_mode"`
	ParticipationYes        float64 `json:"participation_yes"`
	ParticipationNo         float64 `json:"participation_no"`
	ApprovalMode            string  `json:"approval_mode"`
	ApprovalUpper           float64 `json:"approval_upper"`
	ApprovalLower           float64 `json:"approval_lower"`
	Filter                  string  `json:"filter"`
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
	remainingYesVotes, remainingNoVotes = calculateNeededVotesAll(targetApproval, participation, vc, vig)
	return
}

func calculateNeededVotesAll(targetApproval, participation float64, config *VoterConfig, vig *VotesInfoGroup) (neededYesVotes, neededNoVotes float64) {
	me := vig.Me
	var mePool = float64(me.Pool)

	budgetTickets := (participation * mePool) + float64(vig.Public.All())
	ticketsLeftParticipation := budgetTickets - float64(vig.Me.All())
	v := vig.Total()
	yes, no := float64(v.Yes), float64(v.No)
	highestApproval := (ticketsLeftParticipation + yes) / budgetTickets
	lowestApproval := yes / budgetTickets
	if targetApproval > highestApproval {
		neededYesVotes = ticketsLeftParticipation
		neededNoVotes = 0
		fmt.Printf("- target approval greater than highest approval... %v yes votes needed\n", math.Round(ticketsLeftParticipation))
	} else if targetApproval < lowestApproval {
		neededYesVotes = 0
		neededNoVotes = ticketsLeftParticipation
		fmt.Printf("- target approval lesser than lowest approval... %v no votes needed\n", math.Round(ticketsLeftParticipation))
	} else {
		targetYesVotes := targetApproval * budgetTickets
		neededYesVotes = targetYesVotes - yes
		neededNoVotes = (budgetTickets - targetYesVotes) - no
		if neededYesVotes < 0 {
			neededYesVotes = 0
		}
		if neededNoVotes < 0 {
			neededNoVotes = 0
		}
	}
	approvalCalculations(vig, budgetTickets, ticketsLeftParticipation)
	config.printApprovalInfo(vig)
	fmt.Printf("- needed votes: yes: %.f no: %.f total: %.f\n", math.Round(neededYesVotes), math.Round(neededNoVotes),
		math.Round(neededYesVotes)+math.Round(neededNoVotes))
	return
}

func (vc *VoterConfig) printApprovalInfo(vig *VotesInfoGroup) {
	_, boundaryInfo := getTargetRateBoundaries(vig.Public.ApprovalRate(), vc.ApprovalLower, vc.ApprovalUpper)
	minYes, minNo, minPart, total := evaluateMinimumVotes(vig, vc)
	fmt.Printf("- approval target: %s them %.2f%% (%v) reach target yes %.f no %.f total %.f part %.2f%%\n", vc.approvalRange(),
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
	pme := fmt.Sprintf("%.2f%%-%.2f%%", toPercent(lowestApproval), toPercent(highestApproval))

	totalAll, unusedTicketsMe := float64(total.All()), float64(me.Pool)-float64(me.All())
	lowestPossibleApprovalSolo := math.Round(yes/(totalAll+unusedTicketsMe)*10000) / 10000
	highestPossibleApprovalSolo := math.Round(((yes+unusedTicketsMe)/(totalAll+unusedTicketsMe))*10000) / 10000
	ame := fmt.Sprintf("%.2f%%-%.2f%%", toPercent(lowestPossibleApprovalSolo), toPercent(highestPossibleApprovalSolo))

	pool, all := float64(total.Pool), float64(total.All())
	unusedTickets := pool - all
	highestPossibleApproval := (unusedTickets + yes) / pool
	lowestPossibleApproval := yes / pool
	aall := fmt.Sprintf("%.2f%%-%.2f%%", toPercent(roundUpFour(lowestPossibleApproval)),
		toPercent(roundUpFour(highestPossibleApproval)))

	fmt.Printf("- approval calcs: pme %s  ame %s  aall %s\n", pme, ame, aall)
}

func toPercent(value float64) float64 {
	return value * 100
}

func roundUpFour(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func (vc *VoterConfig) approvalRange() string {
	if vc.ApprovalLower == vc.ApprovalUpper {
		return fmt.Sprintf("%.2f%%", vc.ApprovalLower*100)
	}
	return fmt.Sprintf("%.2f%%-%.2f%%", vc.ApprovalLower*100, vc.ApprovalUpper*100)
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
