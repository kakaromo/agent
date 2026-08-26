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
