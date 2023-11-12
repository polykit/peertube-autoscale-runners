package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	_ "github.com/lib/pq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	minRunners, maxRunners, minPending, port                          int
	host, user, password, dbname, runnerPrefix, up, down, metricsAddr string
	db                                                                *sql.DB
	reconcileDelay                                                    time.Duration
	totalGaugeVec, runnersGaugeVec, jobsGaugeVec                      *prometheus.GaugeVec
)

var sqlRunner = `SELECT name FROM "runner"`
var sqlJobs = `SELECT count(id) FROM "runnerJob" WHERE state=$1`
var sqlIdle = `SELECT name FROM "runner" WHERE id NOT IN (
	SELECT DISTINCT(r.id) FROM "runner" r 
		LEFT JOIN "runnerJob" j ON r.id = j."runnerId" WHERE j.state = 2 OR j.state = 9
		ORDER BY r.id) AND name LIKE '` + runnerPrefix + `%' LIMIT 1`

func main() {
	flag.IntVar(&port, "port", 5432, "Database port")
	flag.StringVar(&host, "host", "localhost", "Database host")
	flag.StringVar(&user, "user", "peertube1", "Database user")
	flag.StringVar(&password, "password", "", "Database password")
	flag.StringVar(&dbname, "db", "peertube1", "Database name")
	flag.StringVar(&up, "up", "", "Scale up command")
	flag.StringVar(&down, "down", "", "Scale down command")
	flag.StringVar(&runnerPrefix, "runner-prefix", "runner", "Prefix of runner name")
	flag.IntVar(&minRunners, "min-runners", 0, "Minimum amount of runners")
	flag.IntVar(&maxRunners, "max-runners", 1, "Maximum amount of runners")
	flag.IntVar(&minPending, "min-pending", 10, "Minimum pending jobs before scaling up")
	flag.DurationVar(&reconcileDelay, "reconcile", 5*time.Minute, "Reconcile delay")
	flag.StringVar(&metricsAddr, "listen-address", ":9042", "Metrics port")
	flag.Parse()

	psqlconn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s", host, port, user, password, dbname)
	log.Printf("Connecting to %s:%d database %s", host, port, dbname)

	var err error
	db, err = sql.Open("postgres", psqlconn)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	if err = db.Ping(); err != nil {
		log.Fatalf("Couldn't establish database connection: %s", err)
	}

	totalGaugeVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "peertube_autoscale",
		Name:      "runners_total",
		Help:      "Total peertube runners",
	}, []string{})
	prometheus.MustRegister(totalGaugeVec)

	runnersGaugeVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "peertube_autoscale",
		Name:      "runners_active",
		Help:      "Active peertube runners",
	}, []string{"name"})
	prometheus.MustRegister(runnersGaugeVec)

	jobsGaugeVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "peertube_autoscale",
		Name:      "runners_jobs",
		Help:      "Runner jobs",
	}, []string{"state"})
	prometheus.MustRegister(jobsGaugeVec)

	go func() {
		log.Printf("Starting prometheus metrics on %s", metricsAddr)
		http.Handle("/metrics", promhttp.Handler())
		if err := http.ListenAndServe(metricsAddr, nil); err != nil {
			log.Fatalf("Can't expose metrics: %s", err)
		}
	}()

	ticker := time.NewTicker(reconcileDelay)
	for {
		select {
		case <-ticker.C:
			err = reconcile()
			if err != nil {
				log.Printf("Reconciliation failed: %v", err)
			}
		}
	}
}

func reconcile() error {
	var pending, processing, waiting, completing int
	jobs := make(map[string]int)

	runnerQuery, err := db.Query(sqlRunner)
	if err != nil {
		return err
	}
	defer runnerQuery.Close()

	runnerPendingJobCount := db.QueryRow(sqlJobs, 1)
	if err := runnerPendingJobCount.Scan(&pending); err != nil {
		return err
	}

	runnerProcessingJobCount := db.QueryRow(sqlJobs, 2)
	if err := runnerProcessingJobCount.Scan(&processing); err != nil {
		return err
	}

	runnerWaitingJobCount := db.QueryRow(sqlJobs, 5)
	if err := runnerWaitingJobCount.Scan(&waiting); err != nil {
		return err
	}

	runnerCompletingJobCount := db.QueryRow(sqlJobs, 9)
	if err := runnerCompletingJobCount.Scan(&completing); err != nil {
		return err
	}

	jobs["pending"] = pending
	jobs["processing"] = processing
	jobs["waiting"] = waiting
	jobs["completing"] = completing

	// set metrics
	runners := 0
	runnersGaugeVec.Reset()
	for runnerQuery.Next() {
		var name string
		if err := runnerQuery.Scan(&name); err != nil {
			return err
		}
		rg, err := runnersGaugeVec.GetMetricWithLabelValues(name)
		if err != nil {
			return err
		}
		rg.Inc()
		runners++
	}

	tg, err := totalGaugeVec.GetMetricWithLabelValues()
	if err != nil {
		return err
	}
	tg.Set(float64(runners))

	for label, value := range jobs {
		jg, err := jobsGaugeVec.GetMetricWithLabelValues(label)
		if err != nil {
			return err
		}
		jg.Set(float64(value))
	}

	log.Printf("runners/pending/processing/waiting/completing %d/%d/%d/%d/%d", runners, pending, processing, waiting, completing)

	// scale up
	if pending >= minPending {
		if runners >= maxRunners {
			return nil
		}

		upCmd := exec.Command(up)
		upCmd.Env = os.Environ()
		upCmd.Env = append(upCmd.Env, fmt.Sprintf("RUNNER_NAME=%s", fmt.Sprintf(runnerPrefix+"%d", runners+1)))
		upCmd.Stdout = os.Stdout
		upCmd.Stderr = os.Stderr
		if err := upCmd.Run(); err != nil {
			return fmt.Errorf("Up command failed: %s", err)
		}
		log.Println("Up command successful")

		return nil
	}

	// scale down idle runners (one at a time)
	if runners > minRunners && pending+waiting < minPending {
		var name string
		runnerIdle := db.QueryRow(sqlIdle)
		if err := runnerIdle.Scan(&name); err == sql.ErrNoRows {
			return nil
		} else if err != nil {
			return fmt.Errorf("Couldn't get idle runners: %s", err)
		}

		if name != "" {
			log.Printf("Scaling down runner %s.", name)
			downCmd := exec.Command(down)
			downCmd.Env = os.Environ()
			downCmd.Env = append(downCmd.Env, fmt.Sprintf("RUNNER_NAME=%s", name))
			downCmd.Stdout = os.Stdout
			downCmd.Stderr = os.Stderr
			if err := downCmd.Run(); err != nil {
				return fmt.Errorf("Down command failed: %s", err)
			}
			log.Printf("Deletion of runner %s successfully.", name)
		}

		return nil
	}

	return nil
}
