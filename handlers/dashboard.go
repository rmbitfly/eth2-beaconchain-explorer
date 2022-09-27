package handlers

import (
	"encoding/json"
	"eth2-exporter/db"
	"eth2-exporter/price"
	"eth2-exporter/services"
	"eth2-exporter/templates"
	"eth2-exporter/types"
	"eth2-exporter/utils"
	"fmt"
	"net/http"

	"strconv"
	"strings"

	"github.com/lib/pq"
)

func parseValidatorsFromQueryString(str string, validatorLimit int) ([]uint64, error) {
	if str == "" {
		return []uint64{}, nil
	}

	strSplit := strings.Split(str, ",")
	strSplitLen := len(strSplit)

	// we only support up to 200 validators
	if strSplitLen > validatorLimit {
		return []uint64{}, fmt.Errorf("too much validators")
	}

	validators := make([]uint64, strSplitLen)
	keys := make(map[uint64]bool, strSplitLen)

	for i, vStr := range strSplit {
		v, err := strconv.ParseUint(vStr, 10, 64)
		if err != nil {
			return []uint64{}, err
		}
		// make sure keys are uniq
		if exists := keys[v]; exists {
			continue
		}
		keys[v] = true
		validators[i] = v
	}

	return validators, nil
}

func Dashboard(w http.ResponseWriter, r *http.Request) {

	var dashboardTemplate = templates.GetTemplate("layout.html", "dashboard.html")

	w.Header().Set("Content-Type", "text/html")
	validatorLimit := getUserPremium(r).MaxValidators

	dashboardData := types.DashboardData{}
	dashboardData.ValidatorLimit = validatorLimit

	data := InitPageData(w, r, "dashboard", "/dashboard", "Dashboard")
	data.HeaderAd = true
	data.Data = dashboardData

	err := dashboardTemplate.ExecuteTemplate(w, "layout", data)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Error("error executing template")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
}

// DashboardDataBalance retrieves the income history of a set of validators
func DashboardDataBalance(w http.ResponseWriter, r *http.Request) {
	currency := GetCurrency(r)

	w.Header().Set("Content-Type", "application/json")

	q := r.URL.Query()
	validatorLimit := getUserPremium(r).MaxValidators
	queryValidators, err := parseValidatorsFromQueryString(q.Get("validators"), validatorLimit)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Error("error parsing validators from query string")
		http.Error(w, "Invalid query", 400)
		return
	}
	if err != nil {
		http.Error(w, "Invalid query", 400)
		return
	}
	if len(queryValidators) < 1 {
		http.Error(w, "Invalid query", 400)
		return
	}

	incomeHistoryChartData, err := db.GetValidatorIncomeHistoryChart(queryValidators, currency)
	if err != nil {
		logger.Errorf("failed to genereate income history chart data for dashboard view: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	err = json.NewEncoder(w).Encode(incomeHistoryChartData)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Error("error enconding json response")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
}

func DashboardDataProposals(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	q := r.URL.Query()
	validatorLimit := getUserPremium(r).MaxValidators
	filterArr, err := parseValidatorsFromQueryString(q.Get("validators"), validatorLimit)
	if err != nil {
		http.Error(w, "Invalid query", 400)
		return
	}
	filter := pq.Array(filterArr)

	proposals := []struct {
		Slot   uint64
		Status uint64
	}{}

	err = db.ReaderDb.Select(&proposals, `
		SELECT slot, status
		FROM blocks
		WHERE proposer = ANY($1)
		ORDER BY slot`, filter)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Error("error retrieving block-proposals")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	proposalsResult := make([][]uint64, len(proposals))
	for i, b := range proposals {
		proposalsResult[i] = []uint64{
			uint64(utils.SlotToTime(b.Slot).Unix()),
			b.Status,
		}
	}

	err = json.NewEncoder(w).Encode(proposalsResult)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Error("error enconding json response")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
}

func DashboardDataValidators(w http.ResponseWriter, r *http.Request) {
	currency := GetCurrency(r)

	w.Header().Set("Content-Type", "application/json")

	q := r.URL.Query()
	validatorLimit := getUserPremium(r).MaxValidators
	filterArr, err := parseValidatorsFromQueryString(q.Get("validators"), validatorLimit)
	if err != nil {
		http.Error(w, "Invalid query", 400)
		return
	}
	filter := pq.Array(filterArr)

	var validators []*types.ValidatorsPageDataValidators
	err = db.ReaderDb.Select(&validators, `
		SELECT
			validators.validatorindex,
			validators.pubkey,
			validators.withdrawableepoch,
			validators.balance,
			validators.effectivebalance,
			validators.slashed,
			validators.activationeligibilityepoch,
			validators.lastattestationslot,
			validators.activationepoch,
			validators.exitepoch,
			(SELECT COUNT(*) FROM blocks WHERE proposer = validators.validatorindex AND status = '1') as executedproposals,
			(SELECT COUNT(*) FROM blocks WHERE proposer = validators.validatorindex AND status = '2') as missedproposals,
			COALESCE(validator_performance.performance7d, 0) as performance7d,
			COALESCE(validator_names.name, '') AS name,
		    validators.status AS state
		FROM validators
		LEFT JOIN validator_names ON validators.pubkey = validator_names.publickey
		LEFT JOIN validator_performance ON validators.validatorindex = validator_performance.validatorindex
		WHERE validators.validatorindex = ANY($1)
		LIMIT $2`, filter, validatorLimit)

	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Errorf("error retrieving validator data")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	tableData := make([][]interface{}, len(validators))
	for i, v := range validators {
		tableData[i] = []interface{}{
			fmt.Sprintf("%x", v.PublicKey),
			fmt.Sprintf("%v", v.ValidatorIndex),
			[]interface{}{
				fmt.Sprintf("%.4f %v", float64(v.CurrentBalance)/float64(1e9)*price.GetEthPrice(currency), currency),
				fmt.Sprintf("%.1f %v", float64(v.EffectiveBalance)/float64(1e9)*price.GetEthPrice(currency), currency),
			},
			v.State,
		}

		if v.ActivationEpoch != 9223372036854775807 {
			tableData[i] = append(tableData[i], []interface{}{
				v.ActivationEpoch,
				utils.EpochToTime(v.ActivationEpoch).Unix(),
			})
		} else {
			tableData[i] = append(tableData[i], nil)
		}

		if v.ExitEpoch != 9223372036854775807 {
			tableData[i] = append(tableData[i], []interface{}{
				v.ExitEpoch,
				utils.EpochToTime(v.ExitEpoch).Unix(),
			})
		} else {
			tableData[i] = append(tableData[i], nil)
		}

		if v.WithdrawableEpoch != 9223372036854775807 {
			tableData[i] = append(tableData[i], []interface{}{
				v.WithdrawableEpoch,
				utils.EpochToTime(v.WithdrawableEpoch).Unix(),
			})
		} else {
			tableData[i] = append(tableData[i], nil)
		}

		if v.LastAttestationSlot != nil {
			tableData[i] = append(tableData[i], []interface{}{
				*v.LastAttestationSlot,
				utils.SlotToTime(uint64(*v.LastAttestationSlot)).Unix(),
			})
		} else {
			tableData[i] = append(tableData[i], nil)
		}

		tableData[i] = append(tableData[i], []interface{}{
			v.ExecutedProposals,
			v.MissedProposals,
		})

		// tableData[i] = append(tableData[i], []interface{}{
		// 	v.ExecutedAttestations,
		// 	v.MissedAttestations,
		// })

		// tableData[i] = append(tableData[i], fmt.Sprintf("%.4f ETH", float64(v.Performance7d)/float64(1e9)))
		tableData[i] = append(tableData[i], utils.FormatIncome(v.Performance7d, currency))
	}

	type dataType struct {
		LatestEpoch uint64          `json:"latestEpoch"`
		Data        [][]interface{} `json:"data"`
	}
	data := &dataType{
		LatestEpoch: services.LatestEpoch(),
		Data:        tableData,
	}

	err = json.NewEncoder(w).Encode(data)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Errorf("error enconding json response")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
}

func DashboardDataEarnings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	q := r.URL.Query()
	validatorLimit := getUserPremium(r).MaxValidators
	queryValidators, err := parseValidatorsFromQueryString(q.Get("validators"), validatorLimit)
	if err != nil {
		http.Error(w, "Invalid query", 400)
		return
	}

	earnings, err := GetValidatorEarnings(queryValidators, GetCurrency(r))
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Errorf("error retrieving validator earnings")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
	}

	if earnings == nil {
		earnings = &types.ValidatorEarnings{}
	}

	err = json.NewEncoder(w).Encode(earnings)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Errorf("error enconding json response")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
}

func DashboardDataEffectiveness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	q := r.URL.Query()
	validatorLimit := getUserPremium(r).MaxValidators
	filterArr, err := parseValidatorsFromQueryString(q.Get("validators"), validatorLimit)
	if err != nil {
		logger.Errorf("error retrieving active validators %v", err)
		http.Error(w, "Invalid query", 400)
		return
	}
	filter := pq.Array(filterArr)

	var activeValidators []uint64
	err = db.ReaderDb.Select(&activeValidators, `
		SELECT validatorindex FROM validators where validatorindex = ANY($1) and activationepoch < $2 AND exitepoch > $2
	`, filter, services.LatestEpoch())
	if err != nil {
		logger.Errorf("error retrieving active validators")
	}

	if len(activeValidators) == 0 {
		http.Error(w, "Invalid query", 400)
		return
	}

	var avgIncDistance []float64

	effectiveness, err := db.BigtableClient.GetValidatorEffectiveness(activeValidators, services.LatestEpoch()-1)
	for _, e := range effectiveness {
		avgIncDistance = append(avgIncDistance, e.AttestationEfficiency)
	}
	if err != nil {
		logger.Errorf("error retrieving AverageAttestationInclusionDistance: %v", err)
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	err = json.NewEncoder(w).Encode(avgIncDistance)
	if err != nil {
		logger.Errorf("error enconding json response for %v route: %v", r.URL.String(), err)
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
}

func DashboardDataProposalsHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	q := r.URL.Query()
	validatorLimit := getUserPremium(r).MaxValidators
	filterArr, err := parseValidatorsFromQueryString(q.Get("validators"), validatorLimit)
	if err != nil {
		http.Error(w, "Invalid query", 400)
		return
	}
	filter := pq.Array(filterArr)

	proposals := []struct {
		ValidatorIndex uint64  `db:"validatorindex"`
		Day            int64   `db:"day"`
		Proposed       *uint64 `db:"proposed_blocks"`
		Missed         *uint64 `db:"missed_blocks"`
		Orphaned       *uint64 `db:"orphaned_blocks"`
	}{}

	err = db.ReaderDb.Select(&proposals, `
		SELECT validatorindex, day, proposed_blocks, missed_blocks, orphaned_blocks
		FROM validator_stats
		WHERE validatorindex = ANY($1) AND (proposed_blocks IS NOT NULL OR missed_blocks IS NOT NULL OR orphaned_blocks IS NOT NULL)
		ORDER BY day DESC`, filter)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Error("error retrieving validator_stats")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}

	proposalsHistResult := make([][]uint64, len(proposals))
	for i, b := range proposals {
		var proposed, missed, orphaned uint64 = 0, 0, 0
		if b.Proposed != nil {
			proposed = *b.Proposed
		}
		if b.Missed != nil {
			missed = *b.Missed
		}
		if b.Orphaned != nil {
			orphaned = *b.Orphaned
		}
		proposalsHistResult[i] = []uint64{
			b.ValidatorIndex,
			uint64(utils.DayToTime(b.Day).Unix()),
			proposed,
			missed,
			orphaned,
		}
	}

	err = json.NewEncoder(w).Encode(proposalsHistResult)
	if err != nil {
		logger.WithError(err).WithField("route", r.URL.String()).Error("error enconding json response")
		http.Error(w, "Internal server error", http.StatusServiceUnavailable)
		return
	}
}
