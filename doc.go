// Package speakjet is an emulation of the SpeakJet speech and sound
// synthesizer used in the AtariVox.
//
// This is the second, from-scratch implementation. It was written from the
// SpeakJet User's Manual (Magnevation, revision 1.1, 2004) and from
// recordings of two AtariVoxes playing every sound and control, and from
// nothing else; the aim was the smallest model that matches those recordings.
// It is a line-for-line port of the C library (speakjet.c in
// github.com/andrew-davie/SpeakJet), and renders the same samples.
//
// What the chip was found to do:
//
//   - It makes samples at about 8026 Hz (the two units: 8039 and 8013; the
//     manual says 8192) and holds each until the next. Its own arithmetic
//     assumes 8000: its tones come out rate/8000 sharp. The AtariVox then
//     low-passes the held signal with about two real poles near 2.7 kHz. The
//     output here is at 48 kHz so the images the hold puts up to 20 kHz are
//     carried as the real thing's are.
//   - Voicing: a period of floor(rate / Pitch) + 1 ticks. At the start of each
//     period three formant sines restart and an envelope rises linearly for
//     about 1 ms and falls linearly over about 6 ms; the sum of the sines
//     times the envelope is the voice. The pulse is the same on both units.
//   - Noise: an oscillator whose frequency is thrown about by white noise
//     every sample (the manual's "distortion"); a wide throw makes a flat
//     band, and thrown past the chip's Nyquist it folds back, which is the
//     hiss sounds' peak at 4 kHz.
//   - Bend moves every oscillator by 44 Hz a step; Volume 16 to 127 spans
//     only 6 dB; Fast is x0.8 and Slow x1.25 on length; Speed is linear in
//     length; Stress, Relax and code 18 do nothing measurable; a Repeat's
//     copies each add about the manual's length; every sound ramps in over
//     about 35 ms and out over 30; a sound followed at once by another is cut
//     short.
//
// Each allophone is 14 numbers (tables.go): its kind, measured length, three
// formants with levels, a second target for the glides, and a noise band. Each
// sound effect is one to six "parts" of up to three sweeping tones, a noise
// band, a gate period and a few flags. Both tables were first read off the
// recordings, then refined by fitting against them with a score that compares
// the library to the chip the way two chips compare to each other.
package speakjet
