#!/usr/bin/env bash
# on-device AI 평가 준비 조사 — 기기에서 TTFT/TPOT 를 어떻게 잴 수 있는지 판단한다.
#
# 사용법:
#   scripts/probe-ondevice-ai.sh <serial> [package]
#
# package 를 주면 그 앱을 집중 조사하고, 안 주면 후보를 나열만 한다.
# 읽기 전용 — 기기 상태를 바꾸지 않는다 (drop_caches 도 안 한다).

set -uo pipefail
SERIAL="${1:?사용법: $0 <serial> [package]}"
PKG="${2:-}"
A=(adb -s "$SERIAL")

hr() { printf '\n\033[1m── %s ──\033[0m\n' "$1"; }

hr "1. 기기"
"${A[@]}" shell 'getprop ro.product.model; getprop ro.build.version.release; getprop ro.soc.model 2>/dev/null' 2>&1
echo "root: $("${A[@]}" shell id -u 2>&1 | tr -d '\r')  (0 이어야 drop_caches / fsio 수집 가능)"

hr "2. AI 런타임 후보 패키지"
"${A[@]}" shell 'pm list packages' 2>&1 \
  | grep -iE 'llm|llama|genai|aicore|gemini|mlc|ollama|chat|assistant' | head -20
echo "(비어 있으면 패키지명을 직접 알아내야 한다)"

hr "3. NPU / 가속기 SDK"
SOC=$("${A[@]}" shell 'getprop ro.soc.model 2>/dev/null || getprop ro.board.platform' 2>&1 | tr -d '\r')
echo "SoC: $SOC"
"${A[@]}" shell 'ls /vendor/lib64/ 2>/dev/null' 2>&1 \
  | tr -s ' \r' '\n' | grep -iE 'qnn|htp|hexagon|genie|neuron|apu|mdla|nnapi|ethos|armnn' | head -16

echo
echo "— Qualcomm (QNN/HTP):"
"${A[@]}" shell 'ls /vendor/lib64/libQnn*.so /vendor/lib64/libGenie*.so 2>/dev/null' 2>&1 | tr -d '\r' | head -8
"${A[@]}" shell 'ls /dev/*fastrpc* /dev/adsprpc* 2>/dev/null' 2>&1 | tr -d '\r' | head -4
echo "  libQnnHtp → NPU 경로 · fastrpc/adsprpc 노드 → DSP 통신 채널"
echo "  ⚠ QNN 은 가중치를 DSP 로 넘겨 mmap 하므로, 로드 IO 가 앱 프로세스가 아니라"
echo "     fastrpc/DSP 문맥으로 찍힐 수 있다 — fsio 의 comm 이 앱 이름이 아닐 수 있다."

echo
echo "— MediaTek (NeuroPilot/APU):"
"${A[@]}" shell 'ls /vendor/lib64/libneuron*.so /vendor/lib64/libapu*.so 2>/dev/null' 2>&1 | tr -d '\r' | head -8
"${A[@]}" shell 'ls /dev/apusys* /dev/mdla* /dev/vpu* 2>/dev/null' 2>&1 | tr -d '\r' | head -4
echo "  libneuron_* → NeuroPilot · apusys/mdla → APU 노드"

if [ -n "$PKG" ]; then
  hr "4. [$PKG] 모델 파일 (IO 귀속 앵커)"
  # 큰 파일이 곧 가중치다. 확장자가 런타임을 알려준다.
  "${A[@]}" shell "find /data/data/$PKG /data/user/0/$PKG /sdcard/Android/data/$PKG \
      -type f -size +20M 2>/dev/null | head -20" 2>&1
  echo "  .gguf→llama.cpp  .task/.tflite→MediaPipe  .dlc/.bin→QNN  .mlc→MLC"

  hr "5. [$PKG] 실행 중 프로세스 / 메모리"
  "${A[@]}" shell "ps -A -o PID,RSS,NAME 2>/dev/null | grep -i '$PKG'" 2>&1 | head -5

  hr "6. [$PKG] logcat 타이밍 단서  ← 가장 중요"
  echo "지금부터 15초간 수집합니다. 그 사이 앱에서 프롬프트를 한 번 실행하세요."
  "${A[@]}" logcat -c 2>/dev/null
  "${A[@]}" logcat -v time > /tmp/_ai_logcat.txt 2>&1 &
  LPID=$!
  for i in $(seq 15 -1 1); do printf "\r  남은 시간 %2ds " "$i"; sleep 1; done
  printf "\r                    \r"
  kill $LPID 2>/dev/null; wait $LPID 2>/dev/null

  echo "— 타이밍으로 보이는 줄:"
  grep -iE 'ttft|time to first|prompt eval|eval time|tok/s|tokens per|prefill|decode|latency|ms/tok' \
    /tmp/_ai_logcat.txt | head -25
  echo
  echo "— 벤더 런타임 태그 (QNN/Genie/Neuron):"
  grep -iE 'qnn|genie|htp|hexagon|neuron|apusys|mdla|nnapi' /tmp/_ai_logcat.txt | head -15
  echo
  echo "— 앱 태그 줄 (상위 10):"
  grep -i "$PKG" /tmp/_ai_logcat.txt | head -10
  echo
  echo "전체 로그: /tmp/_ai_logcat.txt ($(wc -l < /tmp/_ai_logcat.txt) 줄)"
else
  hr "4. package 미지정"
  echo "패키지를 정한 뒤 다시 실행하세요:  $0 $SERIAL <package>"
fi

hr "판정 기준"
cat <<'EOF'
  로그에 타이밍이 있다      → 그걸 쓴다. 런타임 자기 시각이라 가장 정확하다.
  모델 파일을 찾았다        → fsio 트레이스에서 그 파일로 IO 를 귀속시킬 수 있다.
  둘 다 없다                → 화면 기준 폴백. TTFT 만 쓰고 TPOT 는 신뢰하지 말 것.

  ⚠ NPU 경로(QNN/APU)일 때:
    - 가중치 로드가 DSP/APU 문맥으로 찍혀 comm 이 앱 이름이 아닐 수 있다.
      → 귀속은 comm 이 아니라 **모델 파일명**으로 걸 것 (nameContains 필터).
    - decode 는 NPU 안에서 도니 IO 도 CPU 도 거의 안 보인다. TPOT 가 나빠도
      우리 트레이스엔 근거가 안 남는다 — 그건 벤더 프로파일러 영역이다.
    - 그래서 이 프로젝트가 확실히 답하는 건 **TTFT 의 IO 몫**이다.
EOF
