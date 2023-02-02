package gostats

import (
	"runtime"
	"strconv"
	"time"

	"github.com/peterbourgon/g2s"
)

// Statsd host:port pair
var Endpoint = "localhost:8125"
// Metric prefix path
var Prefix = "pillar"
// Collection pause interval, in seconds
var Pause = 1
// Collect CPU Statistics
var Cpu = true
// Collect Memory Statistics
var Mem = true
// Collect GC Statistics (requires Memory be enabled)
var Gc = true

// collector
var c *collector = nil

// GaugeFunc is an interface that implements the setting of a gauge value
// in a stats system. It should be expected that key will contain multiple
// parts separated by the '.' character in the form used by statsd (e.x.
// "mem.heap.alloc")
type GaugeFunc func(key string, val uint64)

// Collector implements the periodic grabbing of informational data from the
// runtime package and outputting the values to a GaugeFunc.
type collector struct {
	// PauseDur represents the interval inbetween each set of stats output.
	// Defaults to 10 seconds.
	pauseDur time.Duration

	// EnableCPU determines whether CPU statisics will be output. Defaults to true.
	enableCPU bool

	// EnableMem determines whether memory statistics will be output. Defaults to true.
	enableMem bool

	// EnableGC determines whether garbage collection statistics will be output. EnableMem
	// must also be set to true for this to take affect. Defaults to true.
	enableGC bool

	// Done, when closed, is used to signal Collector that is should stop collecting
	// statistics and the Run function should return. If Done is set, upon shutdown
	// all gauges will be sent a final zero value to reset their values to 0.
	done <-chan struct{}

	gaugeFunc GaugeFunc
}

// New creates a new Collector that will periodically output statistics to gaugeFunc. It
// will also set the values of the exported fields to the described defaults. The values
// of the exported defaults can be changed at any point before Run is called.
func newCollector(gaugeFunc GaugeFunc) *collector {
	return &collector{
		pauseDur:  1 * time.Second,
		enableCPU: true,
		enableMem: true,
		enableGC:  true,
		gaugeFunc: gaugeFunc,
	}
}

// Run gathers statistics from package runtime and outputs them to the configured GaugeFunc every
// PauseDur. This function will not return until Done has been closed (or never if Done is nil),
// therefore it should be called in its own goroutine.
func (c *collector) run() {
	defer c.zeroStats()
	c.outputStats()

	// Gauges are a 'snapshot' rather than a histogram. Pausing for some interval
	// aims to get a 'recent' snapshot out before statsd flushes metrics.
	tick := time.NewTicker(c.pauseDur)
	defer tick.Stop()
	for {
		select {
		case <- c.done:
			return
		case <- tick.C:
			c.outputStats()
		}
	}
}

type cpuStats struct {
	NumGoroutine uint64
	NumCgoCall   uint64
}

// zeroStats sets all the stat guages to zero. On shutdown we want to zero them out so they don't persist
// at their last value until we start back up.
func (c *collector) zeroStats() {
	if c.enableCPU {
		cStats := cpuStats{}
		c.outputCPUStats(&cStats)
	}
	if c.enableMem {
		mStats := runtime.MemStats{}
		c.outputMemStats(&mStats)
		if c.enableGC {
			c.outputGCStats(&mStats)
		}
	}
}

func (c *collector) outputStats() {
	if c.enableCPU {
		cStats := cpuStats{
			NumGoroutine: uint64(runtime.NumGoroutine()),
			NumCgoCall:   uint64(runtime.NumCgoCall()),
		}
		c.outputCPUStats(&cStats)
	}
	if c.enableMem {
		m := &runtime.MemStats{}
		runtime.ReadMemStats(m)
		c.outputMemStats(m)
		if c.enableGC {
			c.outputGCStats(m)
		}
	}
}

func (c *collector) outputCPUStats(s *cpuStats) {
	c.gaugeFunc("cpu.NumGoroutine", s.NumGoroutine)
	c.gaugeFunc("cpu.NumCgoCall", s.NumCgoCall)
}

func (c *collector) outputMemStats(m *runtime.MemStats) {
	// sys
	c.gaugeFunc("mem.sys.Sys", m.Sys)
	c.gaugeFunc("mem.sys.Lookups", m.Lookups)
	c.gaugeFunc("mem.sys.OtherSys", m.OtherSys)

	// common
	c.gaugeFunc("mem.com.Total_VM_Bytes_Reserved", m.Sys)
	c.gaugeFunc("mem.com.Live_Heap_Bytes_Allocated", m.Alloc)
	c.gaugeFunc("mem.com.Cumulative_Heap_Bytes_Allocated", m.TotalAlloc)
	c.gaugeFunc("mem.com.Total_Stack_Allocation", m.StackSys)
	c.gaugeFunc("mem.com.Other_Bytes_Allocation", m.OtherSys)

	// Heap
	c.gaugeFunc("mem.heap.Alloc", m.Alloc)
	c.gaugeFunc("mem.heap.TotalAlloc", m.TotalAlloc)
	c.gaugeFunc("mem.heap.Mallocs", m.Mallocs)
	c.gaugeFunc("mem.heap.Frees", m.Frees)
	c.gaugeFunc("mem.heap.HeapAlloc", m.HeapAlloc)
	c.gaugeFunc("mem.heap.HeapSys", m.HeapSys)
	c.gaugeFunc("mem.heap.HeapIdle", m.HeapIdle)
	c.gaugeFunc("mem.heap.HeapInuse", m.HeapInuse)
	c.gaugeFunc("mem.heap.HeapReleased", m.HeapReleased)
	c.gaugeFunc("mem.heap.HeapObjects", m.HeapObjects)


	// Stack
	c.gaugeFunc("mem.stack.StackSys", m.StackSys)
	c.gaugeFunc("mem.stack.StackInuse", m.StackInuse)
	c.gaugeFunc("mem.stack.MSpanInuse", m.MSpanInuse)
	c.gaugeFunc("mem.stack.MSpanSys", m.MSpanSys)
	c.gaugeFunc("mem.stack.MCacheInuse", m.MCacheInuse)
	c.gaugeFunc("mem.stack.MCacheSys", m.MCacheSys)

}

func (c *collector) outputGCStats(m *runtime.MemStats) {
	c.gaugeFunc("mem.gc.GCSys", m.GCSys)
	c.gaugeFunc("mem.gc.NextGC", m.NextGC)
	c.gaugeFunc("mem.gc.LastGC", m.LastGC)
	c.gaugeFunc("mem.gc.PauseTotalNs", m.PauseTotalNs)
	c.gaugeFunc("mem.gc.Pause", m.PauseNs[(m.NumGC+255)%256])
	c.gaugeFunc("mem.gc.NumGC", uint64(m.NumGC))
}


func Initialize() error {
	statter, err := g2s.Dial("udp", Endpoint)
	if err != nil {
		return err
	}

	if Prefix == "" {
		Prefix = "go"
	}
	Prefix += "."

	gaugeFunc := func(key string, val uint64) {
		statter.Gauge(1.0, Prefix+key, strconv.FormatUint(val, 10))
	}

	c = newCollector(gaugeFunc)
	c.pauseDur = time.Duration(Pause) * time.Second
	c.enableCPU = Cpu
	c.enableMem = Mem
	c.enableGC = Gc

	return nil
}

func Collect() {
	c.run()
}
