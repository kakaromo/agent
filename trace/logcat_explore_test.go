package trace

import (
	"strings"
	"testing"
)

func names(res ExploreResult) []string {
	var out []string
	for _, c := range res.Candidates {
		out = append(out, c.Tag)
	}
	return out
}

func hasTag(res ExploreResult, tag string) bool {
	for _, c := range res.Candidates {
		if c.Tag == tag {
			return true
		}
	}
	return false
}

// 진짜 LLM 로그가 있으면 최상위로 올라와야 한다.
func TestExplore_FindsRealMetrics(t *testing.T) {
	log := strings.Join([]string{
		"100.000  500  500 I SDM     : idle timeout, timeout: 70000 us",
		"100.100  500  500 I sensors-hal: takes 2 ms to receive",
		"101.000  900  900 I Genie   : model load done (1980 ms)",
		"101.500  900  900 I Genie   : first token emitted — TTFT 2840 ms",
		"101.600  900  900 I Genie   : decode 24.1 ms/tok",
		"101.700  900  900 I Genie   : decode 23.8 ms/tok",
	}, "\n")

	res := ExploreLogcat(strings.NewReader(log), ExploreOptions{})
	if len(res.Candidates) == 0 {
		t.Fatal("후보가 없다")
	}
	if res.Candidates[0].Tag != "Genie" {
		t.Errorf("1위가 %q, 기대 Genie (순서: %v)", res.Candidates[0].Tag, names(res))
	}
	if res.Candidates[0].StrongHits < 3 {
		t.Errorf("StrongHits=%d, TTFT/ms-tok 이 잡혀야 한다", res.Candidates[0].StrongHits)
	}
	if res.WeakOnly {
		t.Error("진짜 지표가 있는데 WeakOnly 로 표시됐다")
	}
}

// ⚠ 이 테스트가 이 파일의 핵심이다.
// 음성 wakeword·얼굴인식 같은 다른 온디바이스 ML 은 LLM 과 **어휘가 겹친다** —
// 똑같이 "모델을 로드"하고 "추론"한다. 실기기 로그에서 실제로 wakeword 엔진이
// 최상위를 차지했었다. 낱말만 겹치는 것을 LLM 신호로 착각하면 안 된다.
//
// ⚠ 아래 픽스처는 삼성 기기에서 관측한 실제 줄이지만 **벤더 특정 문제가 아니다.**
// vivo Jovi · Xiaomi XiaoAI · Google Assistant 등 어느 wakeword 엔진이든
// 같은 형태로 걸린다. 태그·문구가 달라도 낱말이 같기 때문이다.
func TestExplore_VoiceMLIsNotMistakenForLLM(t *testing.T) {
	log := strings.Join([]string{
		"100.000 2257 2630 I STHAL   : SoundTriggerHw: loadPhraseSoundModel: 152: Enter",
		"100.100 2992 4190 D SoundTriggerModule: loadPhraseModel()->32",
		"100.200 28614 29005 I BWU@DspControlUtil: Loading sound model 6131373735",
		"100.300 2257 2630 D AGM     : graph: graph_prepare: 920 exit, ret 0",
		"100.400 28614 29005 I DspUtils: DSP Load Event, current active model : wakeword",
		"100.500 6047 6111 I GpsSession_FLP: Time To First Fix   : 0 seconds",
	}, "\n")

	res := ExploreLogcat(strings.NewReader(log), ExploreOptions{})
	for _, c := range res.Candidates {
		if c.StrongHits > 0 {
			t.Errorf("음성 ML/GPS 태그 %q 가 LLM 고유 신호로 잡혔다 (StrongHits=%d, samples=%v)",
				c.Tag, c.StrongHits, c.Samples)
		}
	}
	// 후보가 나오더라도 "강한 신호 없음" 을 반드시 알려야 한다.
	if len(res.Candidates) > 0 && !res.WeakOnly {
		t.Error("LLM 고유 신호가 없는데 WeakOnly 가 꺼져 있다 — 사용자가 목록을 답으로 오인한다")
	}
	joined := strings.Join(res.Diagnosis, "\n")
	if len(res.Candidates) > 0 && !strings.Contains(joined, "한 건도 없다") {
		t.Errorf("진단에 경고가 없다: %v", res.Diagnosis)
	}
}

// ⚠ 코덱·인증처럼 **LLM 과 무관한 도메인**이 토큰/decode 어휘를 쓴다.
// 앞의 wakeword 사례가 "다른 ML 이 같은 낱말을 쓴다" 였다면 이건 한 발 더 나간
// 경우다 — ML 조차 아닌 흔한 시스템 로그가 걸린다. 동영상 재생 한 번이면
// MediaCodec 이 뜨므로 실기기에선 상시 오탐이고, 그러면 WeakOnly 가 영영 안 떠서
// "LLM 신호 0건" 경고가 통째로 죽는다.
func TestExplore_CodecAndAuthAreNotLLM(t *testing.T) {
	log := strings.Join([]string{
		"100.000 1100 1100 I MediaCodec: video decode 8 ms per frame",
		"100.100 1100 1100 D ACodec  : decode latency 12 ms",
		"100.200 1200 1200 I AudioFlinger: decode 5 ms buffer underrun",
		"100.300 1300 1300 I OAuth   : refreshed 2 tokens for account xyz",
		"100.400 1300 1300 D TokenCache: parsed 15 tokens",
		"100.500 1400 1400 I NetworkSecurity: validated 3 tokens",
	}, "\n")

	res := ExploreLogcat(strings.NewReader(log), ExploreOptions{})
	for _, c := range res.Candidates {
		if c.StrongHits > 0 {
			t.Errorf("코덱/인증 태그 %q 가 LLM 고유 신호로 잡혔다 (StrongHits=%d, samples=%v)",
				c.Tag, c.StrongHits, c.Samples)
		}
	}
	if len(res.Candidates) > 0 && !res.WeakOnly {
		t.Error("LLM 고유 신호가 없는데 WeakOnly 가 꺼져 있다 — 안전장치가 죽는다")
	}
}

// 오탐을 막느라 **진짜 신호까지 지우면** 안 된다. 위 좁히기가 과했는지 본다.
// (토큰 문맥이 같은 줄에 있는 형태들 — 순서·표현이 런타임마다 다르다)
func TestExplore_TokenContextStillStrong(t *testing.T) {
	for _, line := range []string{
		"100.000 900 900 I Genie   : decode finished, 128 tokens generated",
		"100.100 900 900 I Runtime : generated 256 tokens in 3.2s",
		"100.200 900 900 I Engine  : prefill 384 tokens",
		"100.300 900 900 I llama   : 24 tokens/s",
		"100.400 900 900 I Genie   : decode 24.1 ms/tok",
	} {
		res := ExploreLogcat(strings.NewReader(line), ExploreOptions{})
		if len(res.Candidates) == 0 || res.Candidates[0].StrongHits == 0 {
			t.Errorf("진짜 LLM 줄인데 강한 신호로 안 잡혔다: %q", line)
		}
	}
}

// keywordRe 의 어간(`generat`/`quantiz`)이 `\b` 에 막혀 죽어 있던 회귀.
// `\b(...|generat|...)\b` 는 "generate" 에 안 걸린다 — 잘린 어간 뒤에
// 단어 경계를 요구하기 때문이다. 실제 낱말로 확인한다.
func TestExplore_KeywordStemsMatchRealWords(t *testing.T) {
	for _, w := range []string{"generate", "generation", "generating", "quantize", "quantized", "quantization"} {
		if !keywordRe.MatchString(w) {
			t.Errorf("keywordRe 가 %q 를 못 잡는다 — 어간 뒤 \\b 때문에 죽은 패턴", w)
		}
	}
}

// GPS 의 `Time To First Fix` 는 TTFT 로 오인되기 쉬운 실제 사례다.
func TestExplore_GpsTimeToFirstFixExcluded(t *testing.T) {
	log := "100.500 6047 6111 I GpsSession_FLP: Time To First Fix   : 0 seconds"
	res := ExploreLogcat(strings.NewReader(log), ExploreOptions{})
	for _, c := range res.Candidates {
		if c.StrongHits > 0 {
			t.Errorf("GPS TTFF 가 LLM 신호로 잡혔다: %+v", c)
		}
	}
}

// 유휴 구간 차분: 추론 때만 나타난 태그를 표시해야 한다.
// ⚠ 단 OnlyDuringRun 만으로 순위가 뒤집히면 안 된다 — 그 구간에만 나타나는
// 일회성 시스템 태그(카메라 종료 등)가 지표 없이 상위를 먹는다.
func TestExplore_IdleDiff(t *testing.T) {
	// ⚠ 일회성 시스템 태그(CameraManagerGlobal)에 unit 히트를 여러 개 준다.
	// OnlyDuringRun 에 단독 가중치(+50)를 주면 이것이 지표 하나 없이 1위가 된다 —
	// 실기기에서 실제로 그랬다. 진짜 신호(Genie)가 이겨야 한다.
	log := strings.Join([]string{
		"10.000  500  500 I Background: idle tick 5 ms",
		"20.000  500  500 I Background: idle tick 5 ms",
	}, "\n")
	// 소음 태그가 지표성 줄을 많이 뿜는 상황을 만든다 (실기기에서 SDM 이 143건이었다).
	var noise []string
	for i := 0; i < 40; i++ {
		noise = append(noise, "20.100  777  777 I CameraManagerGlobal: state changed after 3 ms")
	}
	log += "\n" + strings.Join(noise, "\n") +
		"\n20.200  900  900 I Genie   : decode 24.1 ms/tok"
	res := ExploreLogcat(strings.NewReader(log), ExploreOptions{
		IdleFrom: 9, IdleTo: 11, RunFrom: 19, RunTo: 21,
	})
	if !hasTag(res, "Genie") {
		t.Fatalf("Genie 가 후보에 없다: %v", names(res))
	}
	for _, c := range res.Candidates {
		if c.Tag == "Background" && c.OnlyDuringRun {
			t.Error("유휴 구간에도 있던 태그가 OnlyDuringRun 으로 표시됐다")
		}
		if c.Tag == "Genie" && !c.OnlyDuringRun {
			t.Error("추론 구간에만 있던 태그가 OnlyDuringRun 이 아니다")
		}
	}
	if res.Candidates[0].Tag != "Genie" {
		t.Errorf("1위가 %q — 지표 없는 일회성 태그가 앞서면 안 된다 (%v)",
			res.Candidates[0].Tag, names(res))
	}
}

// 빈 입력·형식 불일치를 원인별로 구분해 알려야 한다.
func TestExplore_Diagnosis(t *testing.T) {
	empty := ExploreLogcat(strings.NewReader(""), ExploreOptions{})
	if !strings.Contains(strings.Join(empty.Diagnosis, " "), "한 줄도") {
		t.Errorf("빈 입력 진단이 없다: %v", empty.Diagnosis)
	}
	garbage := ExploreLogcat(strings.NewReader("not a logcat line\nanother junk"), ExploreOptions{})
	if !strings.Contains(strings.Join(garbage.Diagnosis, " "), "파싱되지 않았다") {
		t.Errorf("형식 불일치 진단이 없다: %v", garbage.Diagnosis)
	}
}
