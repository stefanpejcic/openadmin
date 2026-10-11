package handlers

import (
	"fmt"
	"math"
	"runtime"
	"syscall"
)

// thresholdHost is what the Resource Usage recommendations are based on
type thresholdHost struct {
	Cores                 int
	RAMGB, SwapGB, DiskGB float64
}

// thresholdHostRun is swapped out in tests
var thresholdHostRun = func() thresholdHost {
	const gb = 1024 * 1024 * 1024
	mem := readMemInfo()
	h := thresholdHost{
		Cores:  runtime.NumCPU(),
		RAMGB:  float64(mem["MemTotal"]) / gb,
		SwapGB: float64(mem["SwapTotal"]) / gb,
	}
	var st syscall.Statfs_t
	if syscall.Statfs("/", &st) == nil {
		h.DiskGB = float64(st.Blocks) * float64(st.Bsize) / gb
	}
	return h
}

// thresholdAdvice fills the hint and warning ranges shown under each threshold, matching how sentinel.sh checks them
func thresholdAdvice(v *notifThresholdView, h thresholdHost) {
	switch v.Key {
	case "load":
		rec := 2 * h.Cores
		if rec > 100 {
			// sentinel only accepts 1-100
			v.Hint = fmt.Sprintf("Recommended: 100, the highest value allowed (this server has %d CPU cores).", h.Cores)
			rec = 100
		} else {
			v.Hint = fmt.Sprintf("Recommended: at least %d, 2× the %d CPU cores on this server.", rec, h.Cores)
		}
		v.WarnBelow = rec
		v.WarnLowText = fmt.Sprintf("Below %d, normal load on %d cores can trigger alerts.", rec, h.Cores)
	case "cpu":
		v.Hint = "Recommended: 85-95%. CPU has to stay above it for about 10 minutes before you're alerted."
		v.WarnBelow = 80
		v.WarnLowText = "Below 80%, a busy but healthy server will alert often."
	case "ram":
		v.Hint = fmt.Sprintf("Recommended: 85-95%% of the %.1f GB RAM. Cache isn't counted as used.", h.RAMGB)
		v.WarnBelow = 80
		v.WarnLowText = "Below 80%, normal usage can trigger alerts."
	case "du":
		v.Hint = fmt.Sprintf("Recommended: 85-95%% of the %.0f GB disk on /.", h.DiskGB)
		v.WarnBelow = 70
		v.WarnLowText = "Below 70%, you'll be alerted while there's still plenty of space."
		if h.DiskGB > 0 {
			// leave at least 5 GB, or 10% on small disks, to react to the alert
			reserve := math.Min(5, h.DiskGB*0.1)
			v.WarnAbove = int(math.Floor(100 - reserve*100/h.DiskGB))
			v.WarnHighText = fmt.Sprintf("Above %d%%, less than %.0f GB is left when the alert fires.", v.WarnAbove, reserve)
		}
	case "swap":
		if h.SwapGB == 0 {
			v.Hint = "This server has no swap, so this check is skipped."
			return
		}
		v.Hint = fmt.Sprintf("Recommended: 40-80%% of the %.1f GB swap. Above it, Sentinel clears swap if there's enough free RAM.", h.SwapGB)
		v.WarnBelow = 20
		v.WarnLowText = "Below 20%, swap will be cleared very often."
	}
}
