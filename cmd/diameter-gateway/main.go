// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/3eAI-Labs/diameter-ha-gateway/internal/admin"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/gx"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/gy"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/charging/ro"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/config"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/diameter"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/nats"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/alarm"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/logger"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/platform/monitor"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/service"
	"github.com/3eAI-Labs/diameter-ha-gateway/internal/sms/sgd"
)

func main() {
	// 1. Load Config
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	// 2. Initialize Platform Logger
	logger.InitLogger(cfg.Server.ProductName)
	alarm.Init(cfg.Server.ProductName)
	logger.Info("Starting Diameter HA Gateway", logger.String("product", cfg.Server.ProductName))

	// 3. Initialize Monitor (Prometheus)
	go monitor.Start(8080)

	// 4. Initialize NATS Client
	natsClient, err := nats.NewClient(cfg.Nats)
	if err != nil {
		logger.Error("Failed to connect to NATS", err)
		os.Exit(1)
	}
	defer natsClient.Close()

	// 4.1 Production guard — admin auth MUST be enabled outside dev.
	if cfg.IsProduction() && !cfg.Admin.Auth.Enabled {
		logger.Error("Refusing to start: admin auth disabled in non-dev environment",
			fmt.Errorf("app_env=%s requires admin.auth.enabled=true", cfg.AppEnv))
		os.Exit(2)
	}

	// 4.2 Config reloader — backs the /config/reload endpoint.
	reloader := config.NewReloader(cfg)

	// 5. Initialize Core Components
	wdInterval, _ := time.ParseDuration(cfg.Server.WatchdogInterval)
	wdTimeout, _ := time.ParseDuration(cfg.Server.WatchdogTimeout)
	idleTimeout, _ := time.ParseDuration(cfg.Server.IdlePeerTimeout) // ParseDuration("") == 0 == GC disabled
	peerManager := diameter.NewPeerManagerWith(diameter.PeerManagerOptions{
		WatchdogInterval: wdInterval,
		WatchdogTimeout:  wdTimeout,
		IdlePeerTimeout:  idleTimeout,
		OriginHost:       cfg.Server.OriginHost,
		OriginRealm:      cfg.Server.OriginRealm,
	})

	// 5.1 Gateway Service (MME -> HSS)
	gwService := service.NewGatewayService(natsClient, peerManager, cfg.Server.OriginHost, cfg.Server.OriginRealm)

	// 5.2 Reverse Service (HSS -> MME)
	reverseService := service.NewReverseService(natsClient, peerManager, cfg)
	reverseService.SetReloader(reloader)
	reverseService.StartListening()

	// 5.3 Start Admin API
	if cfg.Admin.Enabled {
		router := &admin.Router{PeerManager: peerManager, Reloader: reloader}
		if cfg.Admin.Auth.Enabled {
			auth, err := admin.NewAuthenticator(context.Background(), cfg.Admin.Auth)
			if err != nil {
				logger.Error("Refusing to start: admin auth misconfigured", err)
				os.Exit(2)
			}
			router.Auth = auth
		} else {
			logger.Warn("Admin API has no authentication (admin.auth.enabled=false)")
		}
		adminRouter := router.Build()
		go func() {
			addr := fmt.Sprintf(":%d", cfg.Admin.Port)
			logger.Info("Admin API listening", logger.String("addr", addr))
			if err := adminRouter.Run(addr); err != nil {
				logger.Error("Admin API failed", err)
			}
		}()
	}

	// 6. Start Diameter Server with PeerManager
	server := diameter.NewServer(cfg, gwService, reverseService, peerManager)
	server.SetReloader(reloader) // hot-reloadable peer whitelist

	// 6.0 Charging Dispatcher (Gx / Gy / Ro share Cmd 272 / App-Id 4 — discriminate by Service-Context-Id).
	// Without this wiring the CCR registration block in diameter/server.go is dead code.
	if cfg.Charging.Enabled {
		chargingTimeout, err := time.ParseDuration(cfg.Charging.RequestTimeout)
		if err != nil || chargingTimeout <= 0 {
			chargingTimeout = 2 * time.Second
		}

		// GxSessionRegistry tracks the Session-Id → PCEF binding established
		// at CCR-I and torn down at CCR-T. The PCRF→PCEF reverse direction
		// (RAR/ASR) consults the registry to resolve which
		// peer holds a session.
		gxRegistry := gx.NewSessionRegistry(gx.RegistryOptions{
			Policy: gx.PolicyFromConfig(cfg.Charging.Gx.SessionEviction),
		})
		// Wire the registry into the PeerManager so connection close clears
		// every session binding for the dropped peer.
		peerManager.AddDropListener(gxRegistry.DropPeer)

		// The Gy and Ro sessions (Application-Id 4) get a registry of their
		// own, under the same eviction policy, for the OCS's RAR and ASR.
		ccRegistry := gx.NewSessionRegistry(gx.RegistryOptions{
			Policy: gx.PolicyFromConfig(cfg.Charging.Gx.SessionEviction),
			App:    "cc",
		})
		peerManager.AddDropListener(ccRegistry.DropPeer)

		gxHandler := gx.NewHandler(natsClient, peerManager, chargingTimeout).
			WithSessionRegistry(gxRegistry)
		dispatcher := charging.NewDispatcher(
			gxHandler,
			gy.NewHandler(natsClient, peerManager, chargingTimeout).WithSessionRegistry(ccRegistry),
			ro.NewHandler(natsClient, peerManager, chargingTimeout).WithSessionRegistry(ccRegistry),
			cfg.Charging.GyContextIDs,
			cfg.Charging.RoContextIDs,
			nil, // no default — unmatched Service-Context-Id rejected with 5012
		).WithIdentity(cfg.Server.OriginHost, cfg.Server.OriginRealm)
		server.SetChargingHandler(dispatcher)
		if len(cfg.Charging.GxContextIDs) > 0 {
			logger.Warn("charging.gx_context_ids is ignored: Gx CCRs are recognised by their Application-Id (16777238)")
		}
		logger.Info("Charging dispatcher enabled (Gx/Gy/Ro)",
			logger.String("timeout", chargingTimeout.String()))

		// Gx reverse-direction service (PCRF → Gateway → PCEF) — RAR/ASR
		// over NATS. Shares the redirect cache with the S6a reverse path so
		// 3006 indications received on either app benefit cross-application
		// routing decisions (cache keys are scoped by Usage, not by app).
		gxReverse := service.NewGxReverseService(natsClient, peerManager,
			gxRegistry, reverseService.RedirectCache(), cfg)
		gxReverse.SetReloader(reloader)
		gxReverse.StartListening()
		server.SetGxAnswerHandler(gxReverse)
		logger.Info("Gx reverse-direction service enabled (RAR/ASR)",
			logger.String("topics", "diameter.gx.{rar,asr}"))

		// Gy/Ro reverse direction (OCS → Gateway → PGW/CTF): RAR and ASR
		// over NATS (TS 32.299 §6.4.4, §6.4.12).
		ccReverse := service.NewCCReverseService(natsClient, peerManager, ccRegistry, cfg)
		server.SetCCAnswerHandler(ccReverse)
		if err := ccReverse.StartListening(); err != nil {
			logger.Error("Failed to start the Gy/Ro reverse-direction service", err)
		} else {
			logger.Info("Gy/Ro reverse-direction service enabled (RAR/ASR)",
				logger.String("topics", "diameter.{gy,ro}.{rar,asr}"))
		}
	}

	// 6.1 Initialize SGd Handler (SMS over Diameter - TS 29.338)
	if cfg.SGd.Enabled {
		sgdTimeout, err := time.ParseDuration(cfg.SGd.RequestTimeout)
		if err != nil {
			sgdTimeout = 2 * time.Second
		}

		// SGd Request Handler (MME/HSS → Gateway → NATS)
		sgdHandler := sgd.NewHandler(natsClient, peerManager, sgdTimeout)
		server.SetSGdHandler(sgdHandler)

		// SGd Client (NATS → Gateway → MME/HSS) - Reverse flow for SMSC-initiated requests
		sgdClient := sgd.NewClient(natsClient, peerManager, cfg, sgdTimeout)
		server.SetSGdAnswerHandler(sgdClient)

		// Start listening for application requests (SRR, TFR, ALR, RDR)
		if err := sgdClient.StartListening(); err != nil {
			logger.Error("Failed to start SGd client", err)
		} else {
			logger.Info("SGd SMS client enabled (reverse flow)",
				logger.String("topics", "diameter.sgd.{srr,tfr,alr,rdr}.request"))
		}

		logger.Info("SGd SMS handler enabled",
			logger.String("timeout", sgdTimeout.String()))
	}

	go func() {
		if err := server.Start(context.Background()); err != nil {
			logger.Error("Diameter Server failed", err)
			os.Exit(1)
		}
	}()

	// 7. Graceful Shutdown — DPR/DPA peer drain.
	// On SIGTERM/SIGINT we send DPR(REBOOTING) to every registered peer and
	// give them up to ShutdownDrainTimeout to flush DPA before the
	// process exits. K8s default termination grace period is 30s, so we
	// keep our own timeout below that to leave headroom for upstream
	// teardown (NATS flush, admin API close).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("Shutting down gracefully...")

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 10*time.Second)
	forced := peerManager.DrainAll(drainCtx, 10*time.Second)
	cancelDrain()
	if forced > 0 {
		logger.Warn("Peers force-closed at shutdown",
			logger.Int("count", forced))
	}
}
