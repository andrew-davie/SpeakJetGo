package speakjet

import "math"

// SampleRate is the rate at which samples are produced. the chip makes its own
// at about 8kHz and holds each until the next, which puts images of the sound
// up to 20kHz and more; the output is at a rate that carries them
const SampleRate = 48000

// the size of the input buffer, and the level above which the Buffer Half
// Full status line is set. from the manual
const (
	BufferSize     = 64
	bufferHalfFull = 32
)

// the rate the chip makes samples at, measured from the images of its tones on
// two units (8039 and 8013 Hz; the manual says 8192). the chip's own arithmetic
// assumes 8000: its tones come out rate/8000 sharp on both units
const (
	rate    = 8026.0
	nominal = 8000.0
)

// the constants of the model, as the recordings gave them. the same values as
// the C library's struct tune
const (
	envRise      = 8.0    // voicing envelope: linear rise, ticks
	envFall      = 50.0   // and fall
	lpfFc        = 2700.0 // the output stage's two poles
	lpfQ         = 0.25
	attackMs     = 35.0 // every sound's level ramp
	releaseMs    = 30.0
	fxAttackMs   = 10.0 // the effects'
	fxReleaseMs  = 15.0
	floor        = 0.10  // the output noise floor, relative
	decayDB      = 35.0  // an fxDecay part falls this many dB
	transitionMs = 25.0  // the voice moves to a new sound's target over this
	closureMs    = 30.0  // silence before a stop's burst
	overlapMs    = 15.0  // a sound followed at once by another is cut short by this
	overlapVMs   = 30.0  // more when both are voiced
	speedA       = 162.0 // length factor (speedA - Speed) / speedB
	speedB       = 48.0
	fxSpeedA     = 149.0 // the effects' (they shorten faster)
	fxSpeedB     = 35.0
	fastShare    = -0.3 // Fast and Slow add this times the manual's length
	slowShare    = 0.35
	copyExtraMs  = 0.0  // a Repeat copy adds this to the manual's length
	bendHz       = 44.0 // Bend step
)

var pauseMs = [7]float64{0, 100, 200, 700, 30, 60, 90}

func ticks(ms float64) int { return int(ms*rate/1000.0 + 0.5) }

// one target the synthesizer moves toward: three voiced formants (Hz, level 0
// to 31) and one noise band (centre Hz, throw Hz, level 0 to 31)
type frame struct {
	f, a       [3]float32
	nf, nw, na float32
}

// a sound is a few segments; each holds or glides to a frame for some ticks
type segment struct {
	ticks    int
	from, to frame
	voiced   bool
	gate     int   // ticks of the on/off period for a pulsed effect, 0 = none
	flags    uint8 // fx*
}

// the sound effects: each is a few parts. a part plays up to three tones
// (plain sines; with fxVoiced the first two are the voice's formants and the
// third stays a plain tone), a noise band, for ms; the tones sweep from fa to
// fb (linearly, or fxExp geometrically, or fxExpDecay settling toward fb);
// fxDecay fades the level over the part, fxBump shapes it as a half sine,
// fxNSweep sweeps the noise band within each gate period; gate pulses it
const (
	fxVoiced   = 1
	fxExp      = 2
	fxDecay    = 4
	fxBump     = 8
	fxExpDecay = 16
	fxNSweep   = 32
	fxPulsed   = 64 // the plain tones pulse with the voice's envelope at Pitch
)

// the tables' units: frequencies in 16 Hz steps, lengths in 5 ms, so every
// field is a byte
const (
	hzStep = 16
	msStep = 5
)

type fxpart struct {
	ms              uint8    // msStep
	fa, fb          [3]uint8 // hzStep
	v               [3]uint8
	nf, nw          uint8 // hzStep
	nv, gate, flags uint8 // gate in ms
}

const maxSegments = 8

// the sound component database: per allophone, its kind, measured length and
// targets (tables.go, generated)
const (
	kVowel = iota
	kGlide
	kNasal
	kVFric
	kFric
	kStop
	kVStop
	kAffr
)

type allophone struct {
	kind   uint8
	ms     uint8    // the length played at Speed 114, measured, msStep
	f      [3]uint8 // formants, hzStep
	a      [3]uint8 // their levels, 0 to 31
	g      [3]uint8 // kGlide: the second target
	nf, nw uint8    // the noise band's centre and throw, hzStep
	na     uint8
	cp     uint8 // the length of each further copy of a Repeat, msStep
}

// the manual's lengths, for the copies of a Repeat
var manualMs = [72]uint8{
	70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70, 70,
	70, 70, 70, 200, 200, 190, 200, 185, 165, 200, 225, 185, 170, 140, 180, 170, 170, 200,
	131, 70, 70, 70, 70, 70, 45, 45, 10, 10, 45, 45, 10, 10, 55, 55, 55, 55,
	70, 70, 70, 70, 70, 40, 40, 50, 40, 50, 70, 170, 55, 55, 55, 45, 99, 99}

// SpeakJet is the chip. Write() is called by whatever is sending it data (the
// AtariVox serial line) and Render() by whatever is playing its output
type SpeakJet struct {
	// the input buffer: a FIFO of raw bytes, as the chip's is
	buf         [BufferSize]uint8
	head, count int
	executing   int // bytes of the command being executed, still counted

	// the controls
	volume, speed, pitch, bend, repeat uint8
	fast, slow, stress, relax          bool

	// what is playing
	seg             [maxSegments]segment
	nseg, cur, tick int
	stick, stotal   int // ticks into the sound, and its whole length
	attack, release int
	copies          int   // copies of a Repeat still to play
	copyOf          int   // which allophone
	last            frame // where the voice is, for transitions into the next sound
	sounding        bool

	// synthesizer state
	period, ptick int
	phase         [3]float64
	nz            float64 // the noise oscillator's phase
	rnd           uint32
	lastr         float64 // the previous noise sample, for the floor's high pass

	// output
	held, heldpos float64
	z             [2]float64
	lpf           [5]float64 // the low pass's coefficients: b0 b1 b2 a1 a2
	speaking      bool

	// the amount of volume increase to apply to each sample. defaults to 1.0
	Gain float32
}

// NewSpeakJet is the preferred method of initialisation for the SpeakJet type
func NewSpeakJet() *SpeakJet {
	sj := &SpeakJet{Gain: 1.0}
	sj.powerOn()
	return sj
}

func (sj *SpeakJet) resetControls() {
	sj.volume, sj.speed, sj.pitch, sj.bend, sj.repeat = 96, 114, 88, 5, 0
	sj.fast, sj.slow, sj.stress, sj.relax = false, false, false, false
}

func (sj *SpeakJet) powerOn() {
	gain := sj.Gain
	*sj = SpeakJet{Gain: gain}
	sj.resetControls()
	sj.rnd = 0x12345678
	// the output low pass: two real poles, bilinear, prewarped
	w := 2.0 * SampleRate * math.Tan(math.Pi*lpfFc/SampleRate)
	k := 2.0 * SampleRate
	d := k*k + w/lpfQ*k + w*w
	sj.lpf[0] = w * w / d
	sj.lpf[1] = 2.0 * sj.lpf[0]
	sj.lpf[2] = sj.lpf[0]
	sj.lpf[3] = (2.0*w*w - 2.0*k*k) / d
	sj.lpf[4] = (k*k - w/lpfQ*k + w*w) / d
}

// CommandSize returns the number of bytes a command takes, including its data
// byte
func CommandSize(code uint8) int {
	switch code {
	case 20, 21, 22, 23, 24, 25, 26, 28, 29, 30:
		return 2
	}
	return 1
}

// Write a command to the input buffer. the data byte is used only by commands
// that take one. returns false if the buffer is too full to take the command,
// in which case it is lost
func (sj *SpeakJet) Write(code uint8, data uint8) bool {
	n := CommandSize(code)
	if sj.count+n > BufferSize {
		return false
	}
	sj.buf[(sj.head+sj.count)%BufferSize] = code
	if n == 2 {
		sj.buf[(sj.head+sj.count+1)%BufferSize] = data
	}
	sj.count += n
	return true
}

// BufferHalfFull is the state of the Buffer Half Full status line: true when
// the buffer holds more than 32 bytes
func (sj *SpeakJet) BufferHalfFull() bool { return sj.count > bufferHalfFull }

// Speaking is the state of the Speaking status line
func (sj *SpeakJet) Speaking() bool { return sj.speaking }

// Reset the chip as though the reset line had been cycled: the buffer is
// cleared and the controls return to their defaults. the sound already
// playing is not cut short
func (sj *SpeakJet) Reset() {
	sj.head, sj.count, sj.executing = 0, 0, 0
	sj.resetControls()
}

// ------------------------------------------------------------- the MSA ----

// the length of a sound at the current Speed, as a factor on its length at
// Speed 114. linear: measured 95/197/287 ms for AW at Speed 127/90/60; the
// effects shorten faster (0.63 at 127, 1.55 at 90, 2.6 at 62)
func (sj *SpeakJet) speedFactor(effect bool) float64 {
	if effect {
		return (fxSpeedA - float64(sj.speed)) / fxSpeedB
	}
	return (speedA - float64(sj.speed)) / speedB
}

// Fast and Slow take away or add a share of the manual's length (measured
// half: AW +35, IH +40, SE +20, EYIY +75 ms under Slow): the manual's "1/2"
// and "1 1/2 times" are of its own length column
func (sj *SpeakJet) modifiedMs(idx int, ms float64) float64 {
	if sj.fast {
		ms += fastShare * float64(manualMs[idx])
	}
	if sj.slow {
		ms += slowShare * float64(manualMs[idx])
	}
	if ms < 10 {
		return 10
	}
	return ms
}

// Bend: every frequency moves by the same amount, 44 Hz a step (D1's 697 and
// 1209 Hz both step by 44). below 5 there is a 22 Hz jump
func (sj *SpeakJet) bendHz() float64 {
	b := bendHz * (float64(sj.bend) - 5)
	if sj.bend < 5 {
		b += bendHz / 2
	}
	return b
}

// the master volume: measured on AW, 16 to 127 is only 6 dB
func (sj *SpeakJet) volumeGain() float64 {
	db := [5]float64{-3.3, -1.3, -0.6, 0.0, 2.5}
	at := [5]float64{16, 32, 64, 96, 127}
	v := float64(sj.volume)
	if v <= 0 {
		return 0.0
	}
	var d float64
	if v < at[0] {
		d = db[0] - (at[0]-v)*0.6
	} else {
		i := 0
		for i < 3 && v > at[i+1] {
			i++
		}
		d = db[i] + (db[i+1]-db[i])*(v-at[i])/(at[i+1]-at[i])
	}
	return math.Pow(10.0, d/20.0)
}

func bent(hz float64, bend float64) float32 {
	if hz > 0 {
		return float32(hz + bend)
	}
	return 0
}

func (sj *SpeakJet) addSegment(ticks int, from, to frame, voiced bool) {
	if sj.nseg >= maxSegments || ticks <= 0 {
		return
	}
	sj.seg[sj.nseg] = segment{ticks: ticks, from: from, to: to, voiced: voiced}
	sj.nseg++
}

// start playing the segments built so far
func (sj *SpeakJet) start(effect bool) {
	if effect {
		sj.attack, sj.release = ticks(fxAttackMs), ticks(fxReleaseMs)
	} else {
		sj.attack, sj.release = ticks(attackMs), ticks(releaseMs)
	}
	sj.cur, sj.tick, sj.stick = 0, 0, 0
	sj.copies = 0
	sj.stotal = 0
	for i := 0; i < sj.nseg; i++ {
		sj.stotal += sj.seg[i].ticks
	}
	sj.sounding = sj.nseg > 0
}

func (sj *SpeakJet) playEffect(code int) {
	first, count := int(fxindex[code-200][0]), int(fxindex[code-200][1])
	bend := sj.bendHz()
	sj.nseg = 0
	for i := 0; i < count; i++ {
		p := &fxparts[first+i]
		var a, b frame
		for k := 0; k < 3; k++ {
			a.f[k] = bent(float64(p.fa[k])*hzStep, bend)
			b.f[k] = bent(float64(p.fb[k])*hzStep, bend)
			a.a[k] = float32(p.v[k])
			b.a[k] = a.a[k]
		}
		a.nf, b.nf = float32(p.nf)*hzStep, float32(p.nf)*hzStep
		a.nw, b.nw = float32(p.nw)*hzStep, float32(p.nw)*hzStep
		a.na, b.na = float32(p.nv), float32(p.nv)
		n := sj.nseg
		sj.addSegment(ticks(float64(p.ms)*msStep*sj.speedFactor(true)), a, b, p.flags&(fxVoiced|fxPulsed) != 0)
		if sj.nseg > n {
			sj.seg[n].gate = ticks(float64(p.gate))
			sj.seg[n].flags = p.flags
		}
	}
	sj.start(true)
}

func alloFrame(a *allophone, second bool, bend float64) frame {
	var f frame
	for k := 0; k < 3; k++ {
		if second {
			f.f[k] = bent(float64(a.g[k])*hzStep, bend)
		} else {
			f.f[k] = bent(float64(a.f[k])*hzStep, bend)
		}
		f.a[k] = float32(a.a[k])
	}
	f.nf, f.nw, f.na = float32(a.nf)*hzStep, float32(a.nw)*hzStep, float32(a.na)
	return f
}

// the segments of one copy of a Repeat: the sound's body for its own copy
// length (about the manual's length for most sounds, not for the stops), no
// transition (the copies run on seamlessly, measured on ZZ x10 and EYIY x5;
// a stop's copies repeat its burst, BE x5 is 260 ms)
func (sj *SpeakJet) copySegments(idx int) int {
	a := &allophones[idx]
	bend := sj.bendHz()
	tk := ticks((float64(a.cp)*msStep + copyExtraMs) * sj.speedFactor(false))
	tgt := alloFrame(a, false, bend)
	sj.nseg = 0
	if a.kind == kGlide {
		sj.addSegment(tk, tgt, alloFrame(a, true, bend), true)
	} else {
		sj.addSegment(tk, tgt, tgt, int(a.a[0])+int(a.a[1])+int(a.a[2]) > 0)
	}
	return tk
}

// expand an allophone into segments: its first play
func (sj *SpeakJet) playAllophone(idx int) {
	a := &allophones[idx]
	bend := sj.bendHz()
	tk := ticks(sj.modifiedMs(idx, float64(a.ms)*msStep) * sj.speedFactor(false))
	tgt := alloFrame(a, false, bend)
	sj.nseg = 0
	var silent frame
	switch a.kind {
	case kVowel, kNasal, kVFric:
		tr := ticks(transitionMs)
		if tr > tk {
			tr = tk
		}
		sj.addSegment(tr, sj.last, tgt, true)
		sj.addSegment(tk-tr, tgt, tgt, true)
	case kGlide:
		tgt2 := alloFrame(a, true, bend)
		tr := ticks(transitionMs)
		if tr > tk/2 {
			tr = tk / 2
		}
		sj.addSegment(tr, sj.last, tgt, true)
		sj.addSegment(tk-tr, tgt, tgt2, true)
	case kFric:
		sj.addSegment(tk, tgt, tgt, false)
	case kStop, kVStop:
		// the closure ("silence already added" says the manual; a voiced stop
		// hums through it on its first formant alone, quietly) then the burst
		// and release, which is the measured length
		bar := silent
		if a.kind == kVStop {
			bar.f[0] = tgt.f[0]
			bar.a[0] = tgt.a[0] / 4.0
		}
		sj.addSegment(ticks(closureMs), bar, bar, a.kind == kVStop)
		sj.addSegment(tk, tgt, tgt, a.kind == kVStop)
	case kAffr:
		// a stop's closure, then the fricative (voiced for JH)
		sj.addSegment(ticks(closureMs), silent, silent, false)
		sj.addSegment(tk, tgt, tgt, int(a.a[0])+int(a.a[1])+int(a.a[2]) > 0)
	}
	sj.start(false)
	// the copies of a Repeat follow on; the level ramps once, over the whole
	sj.copies = 0
	if sj.repeat > 1 {
		sj.copies = int(sj.repeat) - 1
	}
	sj.copyOf = idx
	if sj.copies > 0 {
		nseg, total := sj.nseg, sj.stotal
		saved := sj.seg
		sj.stotal = total + sj.copies*sj.copySegments(idx)
		sj.seg = saved
		sj.nseg = nseg
	}
}

func (sj *SpeakJet) playPause(ms float64) {
	sj.nseg = 0
	var silent frame
	sj.addSegment(ticks(ms), silent, silent, false)
	sj.start(false)
}

func (sj *SpeakJet) peek(i int) uint8 { return sj.buf[(sj.head+i)%BufferSize] }

// take the next command from the buffer and start it. the command stays
// counted in the buffer until it has finished
func (sj *SpeakJet) nextCommand() {
	for sj.count > 0 && !sj.sounding {
		code := sj.peek(0)
		n := CommandSize(code)
		var data uint8
		if n == 2 {
			data = sj.peek(1)
		}
		sj.executing = n
		switch {
		case code <= 6:
			sj.playPause(pauseMs[code])
		case code >= 128 && code <= 199:
			sj.playAllophone(int(code) - 128)
			sj.repeat = 0
			// followed at once by another sound: the next one's start covers
			// this one's end
			if sj.count > n && sj.peek(n) >= 128 && sj.peek(n) <= 254 && sj.nseg > 0 {
				l := &sj.seg[sj.nseg-1]
				next := int(sj.peek(n))
				both := l.voiced && next <= 199 && allophones[next-128].kind != kFric && allophones[next-128].kind != kStop
				cut := ticks(overlapMs)
				if both {
					cut = ticks(overlapVMs)
				}
				if l.ticks > cut+1 {
					l.ticks -= cut
					sj.stotal -= cut
				}
			}
		case code >= 200 && code <= 254:
			sj.playEffect(int(code))
			sj.repeat = 0
		default:
			switch code {
			case 7:
				sj.fast = true
			case 8:
				sj.slow = true
			case 14:
				sj.stress = true
			case 15:
				sj.relax = true
			case 20:
				sj.volume = min(data, 127)
			case 21:
				sj.speed = min(data, 127)
			case 22:
				sj.pitch = data
			case 23:
				sj.bend = min(data, 15)
			case 26:
				sj.repeat = data
			case 30:
				sj.playPause(float64(data) * 10.0)
			case 31:
				sj.resetControls()
			default: // 16 wait, 24/25 ports, 28/29 eeprom, 255: nothing
			}
		}
		if !sj.sounding {
			// the command is done at once
			sj.head = (sj.head + n) % BufferSize
			sj.count -= n
			sj.executing = 0
		}
	}
}

// the sound has ended: retire its command, clear the one-shot modifiers
func (sj *SpeakJet) finishSound() {
	if sj.peek(0) >= 128 {
		sj.fast, sj.slow, sj.stress, sj.relax = false, false, false, false
	}
	sj.head = (sj.head + sj.executing) % BufferSize
	sj.count -= sj.executing
	sj.executing = 0
	sj.sounding = false
}

// --------------------------------------------------------- synthesizer ----

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func (sj *SpeakJet) random() float64 {
	sj.rnd = sj.rnd*1664525 + 1013904223
	return float64(sj.rnd>>8)*(1.0/16777216.0) - 0.5
}

// one chip sample
func (sj *SpeakJet) chipSample() float64 {
	if !sj.sounding {
		sj.nextCommand()
	}
	if !sj.sounding {
		return 0.0
	}

	s := &sj.seg[sj.cur]
	t := 1.0
	if s.ticks > 1 {
		t = float64(sj.tick) / float64(s.ticks-1)
	}
	var f frame
	for k := 0; k < 3; k++ {
		fa, fb := float64(s.from.f[k]), float64(s.to.f[k])
		switch {
		case s.flags&fxExp != 0 && fa > 0 && fb > 0:
			f.f[k] = float32(fa * math.Pow(fb/fa, t))
		case s.flags&fxExpDecay != 0:
			f.f[k] = float32(fb + (fa-fb)*math.Exp(-4.0*t))
		default:
			f.f[k] = float32(lerp(fa, fb, t))
		}
		f.a[k] = float32(lerp(float64(s.from.a[k]), float64(s.to.a[k]), t))
	}
	f.nf = float32(lerp(float64(s.from.nf), float64(s.to.nf), t))
	f.nw = float32(lerp(float64(s.from.nw), float64(s.to.nw), t))
	f.na = float32(lerp(float64(s.from.na), float64(s.to.na), t))
	sj.last = f

	v := 0.0

	// voicing
	if s.voiced && sj.pitch > 0 {
		if sj.ptick <= 0 {
			sj.period = int(rate/float64(sj.pitch)) + 1 // measured: 92 ticks at Pitch 88 on both units
			sj.ptick = sj.period
			if s.flags&fxPulsed == 0 { // pulsed tones run on; formants restart
				sj.phase = [3]float64{}
			}
		}
		age := float64(sj.period - sj.ptick)
		env := 1.0 - (age-envRise)/envFall
		if age < envRise {
			env = age / envRise
		}
		if env < 0.0 {
			env = 0.0
		}
		// an effect's third tone is a plain tone beside the voice
		nf := 3
		if s.flags&fxVoiced != 0 {
			nf = 2
		}
		sum := 0.0
		for k := 0; k < nf; k++ {
			sum += float64(f.a[k]) * math.Sin(2.0*math.Pi*sj.phase[k])
			sj.phase[k] += float64(f.f[k]) / nominal
		}
		if s.flags&fxPulsed != 0 {
			env *= 0.6
		}
		v += env * sum / 128.0
		if nf == 2 {
			v += float64(f.a[2]) / 128.0 * 0.6 * math.Sin(2.0*math.Pi*sj.phase[2])
			sj.phase[2] += float64(f.f[2]) / nominal
		}
		sj.ptick--
	} else {
		sj.ptick = 0
		// plain tones (the effects)
		for k := 0; k < 3; k++ {
			if f.f[k] > 0 && f.a[k] > 0 {
				v += float64(f.a[k]) / 128.0 * 0.6 * math.Sin(2.0*math.Pi*sj.phase[k])
			}
			sj.phase[k] += float64(f.f[k]) / nominal
		}
	}

	// noise: an oscillator whose frequency is thrown about by white noise every
	// sample, within +-nw of nf (the manual's register 6, "disordered with
	// white noise"). with a wide throw the band is flat with sharp edges;
	// thrown past the chip's Nyquist it folds back, which is the hiss sounds'
	// peak at 4 kHz
	if f.na > 0 {
		nf, lvl := float64(f.nf), 1.0
		// fxNSweep: over each gate period the band climbs from 0.4 to 2 times
		// its centre and back, quieter at the top (measured on C8 and C9: 650
		// Hz to 3 kHz and back every 20 ms with a 13 dB dip, both units)
		if s.flags&fxNSweep != 0 && s.gate > 0 {
			sw := math.Sin(math.Pi * float64(sj.tick%s.gate) / float64(s.gate))
			nf *= 0.4 + 1.6*sw
			lvl = 1.0 - 0.75*sw
		}
		sj.nz += (nf + float64(f.nw)*2.0*sj.random()) / nominal
		v += lvl * float64(f.na) / 128.0 * 0.7 * math.Sin(2.0*math.Pi*sj.nz)
	}

	if s.gate > 0 && sj.tick%s.gate >= s.gate/2 {
		v = 0.0
	}
	if s.flags&fxDecay != 0 {
		v *= math.Pow(10.0, -decayDB*t/20.0)
	}
	if s.flags&fxBump != 0 {
		v *= math.Sin(math.Pi * t)
	}

	// the sound's attack and release
	if sj.stick < sj.attack {
		v *= float64(sj.stick) / float64(sj.attack)
	}
	if sj.stotal-sj.stick < sj.release {
		v *= float64(sj.stotal-sj.stick) / float64(sj.release)
	}
	sj.stick++

	v *= sj.volumeGain()
	// the output noise floor: white under the effects' tones, rising with
	// frequency under the voice (the valley between the first two formants is
	// deeper on the chip than a white floor leaves it)
	r := sj.random()
	if s.voiced && s.flags&(fxVoiced|fxPulsed) == 0 {
		v *= 1.0 + floor*2.0*(r-sj.lastr)
	} else {
		v *= 1.0 + floor*2.0*r
	}
	sj.lastr = r

	sj.tick++
	if sj.tick >= s.ticks {
		sj.tick = 0
		sj.cur++
		if sj.cur >= sj.nseg {
			if sj.copies > 0 {
				sj.copies--
				sj.copySegments(sj.copyOf)
				sj.cur = 0
			} else {
				sj.finishSound()
			}
		}
	}
	return v
}

// Render fills buf with the next len(buf) samples of output, at SampleRate,
// in the range -1 to 1. returns true if the chip is producing sound
func (sj *SpeakJet) Render(buf []float32) bool {
	const step = rate / SampleRate
	b0, b1, b2, a1, a2 := sj.lpf[0], sj.lpf[1], sj.lpf[2], sj.lpf[3], sj.lpf[4]
	sounding := sj.sounding || sj.count > 0
	for i := range buf {
		// the average of the held chip samples over this output sample
		rest, sum := step, 0.0
		for sj.heldpos+rest >= 1.0 {
			sum += sj.held * (1.0 - sj.heldpos)
			rest -= 1.0 - sj.heldpos
			sj.held = sj.chipSample()
			sj.heldpos = 0.0
			sounding = sounding || sj.sounding || sj.count > 0
		}
		sum += sj.held * rest
		sj.heldpos += rest
		x := sum / step
		// direct form II transposed
		y := b0*x + sj.z[0]
		sj.z[0] = b1*x - a1*y + sj.z[1]
		sj.z[1] = b2*x - a2*y
		buf[i] = float32(y) * sj.Gain
	}
	sj.speaking = sounding
	return sounding
}
