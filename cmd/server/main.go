package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/lineleader/lineleader/internal/dvc"
	"github.com/lineleader/lineleader/internal/web"
)

func main() {
	dataDir := flag.String("data-dir", "data/point-charts", "directory with JSON chart files")
	configFile := flag.String("config", dvc.DefaultConfigPath(), "app config file (JSON)")
	plansFile := flag.String("plans", dvc.DefaultPlansPath(), "plans file (JSON)")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	charts, err := dvc.LoadAll(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading charts: %v\n", err)
		os.Exit(1)
	}
	if len(charts) == 0 {
		fmt.Fprintf(os.Stderr, "no charts found in %s — run 'dvc import' first\n", *dataDir)
		os.Exit(1)
	}

	cfg, err := dvc.LoadConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading config %s: %v\n", *configFile, err)
	}

	plans, _ := dvc.LoadPlans(*plansFile)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	srv := web.NewServer(web.Options{
		Charts:     charts,
		Config:     cfg,
		ConfigPath: *configFile,
		Plans:      plans,
		PlansPath:  *plansFile,
		Defaults: web.Defaults{
			From:      today.Format("2006-01-02"),
			To:        today.AddDate(0, 0, 14).Format("2006-01-02"),
			Budget:    "100",
			MinNights: "1",
		},
	})

	log.Printf("lineleader web listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv))
}
