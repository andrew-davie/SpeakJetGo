package speakjet

import (
	"math"
	"testing"
)

// every sound code plays something: the allophones' lengths are within what
// the chip was measured to play, and every effect has parts
func TestSounds(t *testing.T) {
	for code := 128; code <= 199; code++ {
		a := &allophones[code-128]
		if int(a.ms)*msStep < 10 || int(a.ms)*msStep > 320 {
			t.Errorf("code %d: length %d ms", code, a.ms)
		}
	}
	for code := 200; code <= 254; code++ {
		ms := 0
		first, count := int(fxindex[code-200][0]), int(fxindex[code-200][1])
		for i := 0; i < count; i++ {
			ms += int(fxparts[first+i].ms) * msStep
		}
		if ms <= 0 {
			t.Errorf("code %d: no length", code)
		}
	}
}

func TestPhraseLength(t *testing.T) {
	sj := NewSpeakJet()
	for _, c := range []uint8{183, 135, 199} { // HO OH PO
		sj.Write(c, 0)
	}
	buf := make([]float32, 1)
	n := 0
	for sj.Render(buf) {
		n++
	}
	got := float64(n) * 1000 / SampleRate
	// the sounds' lengths, the stop's closure, and the two sounds followed at
	// once by another cut short by the overlap
	want := float64(int(allophones[183-128].ms)+int(allophones[135-128].ms)+int(allophones[199-128].ms))*msStep + closureMs - 2*overlapMs
	if math.Abs(got-want) > 10 {
		t.Errorf("phrase played for %.0fms, expected about %.0fms", got, want)
	}
}

func TestBuffer(t *testing.T) {
	sj := NewSpeakJet()
	for i := 0; i < BufferSize; i++ {
		if !sj.Write(128, 0) {
			t.Fatalf("buffer refused byte %d", i)
		}
		if sj.BufferHalfFull() != (i+1 > bufferHalfFull) {
			t.Errorf("after %d bytes buffer half full is %v", i+1, sj.BufferHalfFull())
		}
	}
	if sj.Write(128, 0) {
		t.Error("buffer accepted a 65th byte")
	}
	if sj.Write(21, 100) {
		t.Error("buffer accepted a two byte command when full")
	}
	// draining the buffer clears the status line
	buf := make([]float32, 256)
	for i := 0; i < 10000 && (sj.Render(buf) || sj.count > 0); i++ {
	}
	if sj.BufferHalfFull() || sj.count != 0 {
		t.Errorf("buffer not empty after rendering: %d bytes", sj.count)
	}
}

func TestOutputRange(t *testing.T) {
	sj := NewSpeakJet()
	buf := make([]float32, 256)
	for c := 128; c <= 254; c++ {
		sj.Write(uint8(c), 0)
		for i := 0; i < 10000; i++ {
			sounding := sj.Render(buf)
			for _, v := range buf {
				if math.IsNaN(float64(v)) || v > 1.5 || v < -1.5 {
					t.Fatalf("code %d: sample out of range: %f", c, v)
				}
			}
			if !sounding && sj.count == 0 {
				break
			}
		}
	}
}
