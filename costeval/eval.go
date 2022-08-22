package costeval

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/qw4990/tidb-cost-calibrator/utils"
)

func CostEval() {
	opt := utils.Option{
		Addr:     "172.16.5.173",
		Port:     4000,
		User:     "root",
		Password: "",
		Label:    "",
	}
	ins := utils.MustConnectTo(opt)
	costEval(ins, &evalOpt{"synthetic", 2, 1, 5})
}

type evalOpt struct {
	db           string
	costModelVer int
	repeatTimes  int
	numPerQuery  int
}

func (opt *evalOpt) genInitSQLs() []string {
	return []string{
		fmt.Sprintf(`use %v`, opt.db),
		`set @@tidb_distsql_scan_concurrency=1`,
		`set @@tidb_executor_concurrency=1`,
		`set @@tidb_opt_tiflash_concurrency_factor=1`,
		`set @@tidb_enable_new_cost_interface=1`,
		fmt.Sprintf(`set @@tidb_cost_model_version=%v`, opt.costModelVer),
	}
}

func costEval(ins utils.Instance, opt *evalOpt) {
	info("start cost evaluation, db=%v, ver=%v", opt.db, opt.costModelVer)
	var qs utils.Queries
	dataDir := "./data"
	queryFile := filepath.Join(dataDir, fmt.Sprintf("%v-queries.json", opt.db))
	if err := utils.ReadFrom(queryFile, &qs); err != nil {
		qs = genSYNQueries(ins, "synthetic", opt.numPerQuery)
		utils.SaveTo(queryFile, qs)
		info("generate %v queries successfully", len(qs))
	} else {
		info("read %v queries successfully", len(qs))
	}

	var rs utils.Records
	recordFile := filepath.Join(dataDir, fmt.Sprintf("%v-%v-records.json", opt.db, opt.costModelVer))
	if err := utils.ReadFrom(recordFile, &rs); err != nil {
		rs = runEvalQueries(ins, opt, qs)
		utils.SaveTo(recordFile, rs)
		info("generate %v records successfully", len(rs))
	} else {
		info("read %v records successfully", len(rs))
	}

	sort.Slice(rs, func(i, j int) bool {
		return rs[i].TimeMS < rs[j].TimeMS
	})

	for _, r := range rs {
		info("record %vms \t %.2f \t %v \t %v", r.TimeMS, r.Cost, r.Label, r.SQL)
	}
	utils.DrawCostRecordsTo(rs, fmt.Sprintf("./data/%v-%v-scatter.png", opt.db, opt.costModelVer))
}

func runEvalQueries(ins utils.Instance, opt *evalOpt, qs utils.Queries) utils.Records {
	for _, sql := range opt.genInitSQLs() {
		ins.MustExec(sql)
	}

	var rs utils.Records
	beginAt := time.Now()
	for i, q := range qs {
		info("run %v/%v tot=%v, q=%v", i, len(qs), q.SQL, time.Since(beginAt))
		explain := `explain analyze format='true_card_cost' ` + q.SQL
		var cost, totTimeMS float64
		for k := 0; k < opt.repeatTimes; k++ {
			rs := ins.MustQuery(explain)
			r := utils.ParseExplainAnalyzeResultsWithRows(rs)
			if k == 0 {
				cost = r.PlanCost
			} else if cost != r.PlanCost { // the plan changes
				panic(fmt.Sprintf("q=%v, cost=%v, new-cost=%v", explain, cost, r.PlanCost))
			}
			totTimeMS += r.TimeMS
		}
		avgTimeMS := totTimeMS / float64(opt.repeatTimes)
		rs = append(rs, utils.Record{
			Cost:   cost,
			TimeMS: avgTimeMS,
			Label:  q.Label,
			SQL:    q.SQL,
		})
	}
	return rs
}

func info(format string, args ...interface{}) {
	fmt.Printf("[cost-eval] %v\n", fmt.Sprintf(format, args...))
}