package main

import (
	"eth2-exporter/db"
	"eth2-exporter/metrics"
	"eth2-exporter/types"
	"eth2-exporter/userService"
	"eth2-exporter/utils"
	"eth2-exporter/version"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"

	_ "eth2-exporter/docs"
	_ "net/http/pprof"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	configPath := flag.String("config", "", "Path to the config file, if empty string defaults will be used")

	flag.Parse()

	cfg := &types.Config{}
	err := utils.ReadConfig(cfg, *configPath)
	if err != nil {
		logrus.Fatalf("error reading config file: %v", err)
	}
	utils.Config = cfg
	logrus.WithFields(logrus.Fields{
		"config":    *configPath,
		"version":   version.Version,
		"chainName": utils.Config.Chain.Config.ConfigName}).Printf("starting")

	if utils.Config.Chain.Config.SlotsPerEpoch == 0 || utils.Config.Chain.Config.SecondsPerSlot == 0 {
		utils.LogFatal(err, "invalid chain configuration specified, you must specify the slots per epoch, seconds per slot and genesis timestamp in the config file", 0)
	}

	if utils.Config.Pprof.Enabled {
		go func() {
			logrus.Infof("starting pprof http server on port %s", utils.Config.Pprof.Port)
			logrus.Info(http.ListenAndServe(fmt.Sprintf("0.0.0.0:%s", utils.Config.Pprof.Port), nil))
		}()
	}

	wg := &sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		db.MustInitFrontendDB(&types.DatabaseConfig{
			Username:     cfg.Frontend.WriterDatabase.Username,
			Password:     cfg.Frontend.WriterDatabase.Password,
			Name:         cfg.Frontend.WriterDatabase.Name,
			Host:         cfg.Frontend.WriterDatabase.Host,
			Port:         cfg.Frontend.WriterDatabase.Port,
			MaxOpenConns: cfg.Frontend.WriterDatabase.MaxOpenConns,
			MaxIdleConns: cfg.Frontend.WriterDatabase.MaxIdleConns,
		}, &types.DatabaseConfig{
			Username:     cfg.Frontend.ReaderDatabase.Username,
			Password:     cfg.Frontend.ReaderDatabase.Password,
			Name:         cfg.Frontend.ReaderDatabase.Name,
			Host:         cfg.Frontend.ReaderDatabase.Host,
			Port:         cfg.Frontend.ReaderDatabase.Port,
			MaxOpenConns: cfg.Frontend.ReaderDatabase.MaxOpenConns,
			MaxIdleConns: cfg.Frontend.ReaderDatabase.MaxIdleConns,
		})
	}()

	// if needed, init the database, cache or bigtable

	wg.Add(1)
	go func() {
		defer wg.Done()
		bt, err := db.InitBigtable(utils.Config.Bigtable.Project, utils.Config.Bigtable.Instance, fmt.Sprintf("%d", utils.Config.Chain.Config.DepositChainID), utils.Config.RedisCacheEndpoint)
		if err != nil {
			logrus.Fatalf("error connecting to bigtable: %v", err)
		}
		db.BigtableClient = bt
	}()

	wg.Wait()

	defer db.FrontendReaderDB.Close()
	defer db.FrontendWriterDB.Close()

	if utils.Config.Metrics.Enabled {
		go metrics.MonitorDB(db.WriterDb)
		DBInfo := []string{
			cfg.WriterDatabase.Username,
			cfg.WriterDatabase.Password,
			cfg.WriterDatabase.Host,
			cfg.WriterDatabase.Port,
			cfg.WriterDatabase.Name}
		DBStr := strings.Join(DBInfo, "-")
		frontendDBInfo := []string{
			cfg.Frontend.WriterDatabase.Username,
			cfg.Frontend.WriterDatabase.Password,
			cfg.Frontend.WriterDatabase.Host,
			cfg.Frontend.WriterDatabase.Port,
			cfg.Frontend.WriterDatabase.Name}
		frontendDBStr := strings.Join(frontendDBInfo, "-")
		if DBStr != frontendDBStr {
			go metrics.MonitorDB(db.FrontendWriterDB)
		}
	}

	logrus.Infof("database connection established")

	userService.Init()

	utils.WaitForCtrlC()

	logrus.Println("exiting...")
}
