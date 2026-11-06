package analyzer

import "testing"

func TestDetectSpikes(t *testing.T) {
	series := make([]float64, 60)
	for i := range series {
		series[i] = float64(10 + i%3) // 平稳流量 10~12
	}
	series[45] = 80 // 明显突增
	spikes := DetectSpikes(series, 30, 3, 5)
	if len(spikes) != 1 || spikes[0].Index != 45 {
		t.Fatalf("expected one spike at 45, got %+v", spikes)
	}
	if Severity(spikes[0].Score, 3) != "high" {
		t.Errorf("expected high severity, got %s (score %.2f)", Severity(spikes[0].Score, 3), spikes[0].Score)
	}
}

func TestDetectSpikesIgnoresNoise(t *testing.T) {
	series := []float64{0, 1, 0, 2, 1, 0, 3, 1, 0, 2, 1, 4, 0, 1, 2}
	if s := DetectSpikes(series, 10, 3, 5); len(s) != 0 {
		t.Fatalf("expected no spikes, got %+v", s)
	}
}

func TestDetectSpikesColdStart(t *testing.T) {
	series := []float64{100, 0, 0, 0}
	if s := DetectSpikes(series, 10, 3, 5); len(s) != 0 {
		t.Fatalf("cold start should not alert, got %+v", s)
	}
}

func TestMergeSpikes(t *testing.T) {
	spikes := []Spike{{Index: 10, Observed: 5, Score: 4}, {Index: 11, Observed: 8, Score: 9}, {Index: 13, Observed: 6, Score: 5}, {Index: 30, Observed: 7, Score: 6}}
	inc := MergeSpikes(spikes, 2)
	if len(inc) != 2 {
		t.Fatalf("expected 2 incidents, got %+v", inc)
	}
	if inc[0].Start != 10 || inc[0].End != 14 || inc[0].Observed != 19 || inc[0].Score != 9 {
		t.Errorf("bad first incident %+v", inc[0])
	}
}
