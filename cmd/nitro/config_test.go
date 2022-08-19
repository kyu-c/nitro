// Copyright 2021-2022, Offchain Labs, Inc.
// For license information, see https://github.com/nitro/blob/master/LICENSE

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/offchainlabs/nitro/util/colors"
	"github.com/offchainlabs/nitro/util/testhelpers"
)

func TestSeqConfig(t *testing.T) {
	args := strings.Split("--persistent.chain /tmp/data --init.dev-init --node.l1-reader.enable=false --l1.chain-id 5 --l2.chain-id 421613 --l1.wallet.pathname /l1keystore --l1.wallet.password passphrase --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --node.sequencer.enable --node.feed.output.enable --node.feed.output.port 9642", " ")
	_, _, _, _, _, err := ParseNode(context.Background(), args)
	Require(t, err)
}

func TestUnsafeStakerConfig(t *testing.T) {
	args := strings.Split("--persistent.chain /tmp/data --init.dev-init --node.l1-reader.enable=false --l1.chain-id 5 --l2.chain-id 421613 --l1.wallet.pathname /l1keystore --l1.wallet.password passphrase --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --node.validator.enable --node.validator.strategy MakeNodes --node.validator.staker-interval 10s --node.forwarding-target null --node.validator.dangerous.without-block-validator", " ")
	_, _, _, _, _, err := ParseNode(context.Background(), args)
	Require(t, err)
}

func TestValidatorConfig(t *testing.T) {
	args := strings.Split("--persistent.chain /tmp/data --init.dev-init --node.l1-reader.enable=false --l1.chain-id 5 --l2.chain-id 421613 --l1.wallet.pathname /l1keystore --l1.wallet.password passphrase --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --node.validator.enable --node.validator.strategy MakeNodes --node.validator.staker-interval 10s --node.forwarding-target null", " ")
	_, _, _, _, _, err := ParseNode(context.Background(), args)
	Require(t, err)
}

func TestAggregatorConfig(t *testing.T) {
	args := strings.Split("--persistent.chain /tmp/data --init.dev-init --node.l1-reader.enable=false --l1.chain-id 5 --l2.chain-id 421613 --l1.wallet.pathname /l1keystore --l1.wallet.password passphrase --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --node.sequencer.enable --node.feed.output.enable --node.feed.output.port 9642 --node.data-availability.enable --node.data-availability.rpc-aggregator.backends {[\"url\":\"http://localhost:8547\",\"pubkey\":\"abc==\",\"signerMask\":0x1]}", " ")
	_, _, _, _, _, err := ParseNode(context.Background(), args)
	Require(t, err)
}

func TestReloads(t *testing.T) {
	var check func(node reflect.Value, cold bool, path string)
	check = func(node reflect.Value, cold bool, path string) {
		if node.Kind() != reflect.Struct {
			return
		}

		for i := 0; i < node.NumField(); i++ {
			hot := node.Type().Field(i).Tag.Get("reload") == "hot"
			dot := path + "." + node.Type().Field(i).Name
			if hot && cold {
				t.Fatalf(fmt.Sprintf(
					"Option %v%v%v is reloadable but %v%v%v is not",
					colors.Red, dot, colors.Clear,
					colors.Red, path, colors.Clear,
				))
			}
			if hot {
				colors.PrintBlue(dot)
			}
			check(node.Field(i), !hot, dot)
		}
	}

	config := NodeConfigDefault
	update := NodeConfigDefault
	update.Node.Sequencer.MaxBlockSpeed++

	check(reflect.ValueOf(config), false, "config")
	Require(t, config.CanReload(&config))
	Require(t, config.CanReload(&update))

	testUnsafe := func() {
		if config.CanReload(&update) == nil {
			Fail(t, "failed to detect unsafe reload")
		}
		update = NodeConfigDefault
	}

	// check that non-reloadable fields fail assignment
	update.Metrics = !update.Metrics
	testUnsafe()
	update.L2.ChainID++
	testUnsafe()
	update.Node.Sequencer.ForwardTimeout++
	testUnsafe()
}

func TestLiveNodeConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// create empty config file
	configFile := filepath.Join(t.TempDir(), "config.json")
	Require(t, os.WriteFile(configFile, []byte("{}"), 0600))

	args := strings.Split("--persistent.chain /tmp/data --init.dev-init --node.l1-reader.enable=false --l1.chain-id 5 --l2.chain-id 421613 --l1.wallet.pathname /l1keystore --l1.wallet.password passphrase --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --node.sequencer.enable --node.feed.output.enable --node.feed.output.port 9642", " ")
	args = append(args, []string{"--conf.file", configFile}...)
	config, _, _, _, _, err := ParseNode(context.Background(), args)
	Require(t, err)

	liveConfig := NewLiveNodeConfig(args, config)
	liveConfig.Start(ctx)

	// check that reloading the config doesn't change anything
	Require(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))
	time.Sleep(20 * time.Millisecond)
	if !reflect.DeepEqual(liveConfig.get(), config) {
		Fail(t, "live config differs from expected")
	}

	// check updating the config
	update := config.ShallowClone()
	expected := config.ShallowClone()
	update.Node.Sequencer.MaxBlockSpeed += 2 * time.Millisecond
	expected.Node.Sequencer.MaxBlockSpeed += 2 * time.Millisecond
	Require(t, liveConfig.set(update))
	if !reflect.DeepEqual(liveConfig.get(), expected) {
		Fail(t, "failed to set config")
	}

	// check that an invalid reload gets rejected
	update = config.ShallowClone()
	update.L2.ChainID++
	if liveConfig.set(update) == nil {
		Fail(t, "failed to reject invalid update")
	}
	if !reflect.DeepEqual(liveConfig.get(), expected) {
		Fail(t, "config should not change if its update fails")
	}

	// change the config file
	expected = config.ShallowClone()
	expected.Node.Sequencer.MaxBlockSpeed += time.Millisecond
	jsonConfig := fmt.Sprintf("{\"node\":{\"sequencer\":{\"max-block-speed\":\"%s\"}}}", expected.Node.Sequencer.MaxBlockSpeed.String())
	Require(t, os.WriteFile(configFile, []byte(jsonConfig), 0600))

	// trigger LiveConfig reload
	Require(t, syscall.Kill(syscall.Getpid(), syscall.SIGUSR1))

	for i := 0; i < 16; i++ {
		config = liveConfig.get()
		if reflect.DeepEqual(config, expected) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	Fail(t, "failed to update config", config.Node.Sequencer.MaxBlockSpeed, update.Node.Sequencer.MaxBlockSpeed)
}

func Require(t *testing.T, err error, text ...interface{}) {
	t.Helper()
	testhelpers.RequireImpl(t, err, text...)
}

func Fail(t *testing.T, printables ...interface{}) {
	t.Helper()
	testhelpers.FailImpl(t, printables...)
}
