// Command gen — scenario.Specs 에서 UI 용 step 계약 파일을 생성한다.
//
//	go run ./scenario/gen
//
// 생성 대상: ui/src/routes/agent/scenario-canvas/step-contract.ts
//
// 예전엔 팔레트 항목과 색상 맵이 Go 실행부와 따로 손으로 관리돼서, 새 step 을
// 추가하면 UI 에 안 나타나거나 색이 빠지는 일이 있었다. 이제 Go 계약에서 파생한다.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agent/scenario"
)

func main() {
	var b strings.Builder

	b.WriteString(`// 이 파일은 자동 생성됩니다. 직접 수정하지 마세요.
// 원본: scenario/steptypes.go (Go 실행부 계약)
// 재생성: go run ./scenario/gen

export interface StepParamSpec {
	name: string;
	required: boolean;
	enum?: string[];
	default?: string;
	desc: string;
}

export interface StepContract {
	type: string;
	label: string;
	desc: string;
	icon: string;
	color: string;
	destructive: boolean;
	requiresTool: boolean;
	aiUsable: boolean;
	params: StepParamSpec[];
}

export const STEP_CONTRACTS: StepContract[] = [
`)

	for _, s := range scenario.Specs {
		b.WriteString("\t{\n")
		fmt.Fprintf(&b, "\t\ttype: %q,\n", s.Type)
		fmt.Fprintf(&b, "\t\tlabel: %q,\n", s.Label)
		fmt.Fprintf(&b, "\t\tdesc: %q,\n", s.Desc)
		fmt.Fprintf(&b, "\t\ticon: %q,\n", s.Icon)
		fmt.Fprintf(&b, "\t\tcolor: %q,\n", s.Color)
		fmt.Fprintf(&b, "\t\tdestructive: %t,\n", s.Destructive)
		fmt.Fprintf(&b, "\t\trequiresTool: %t,\n", s.RequiresTool)
		fmt.Fprintf(&b, "\t\taiUsable: %t,\n", s.AIUsable)
		b.WriteString("\t\tparams: [")
		if len(s.Params) == 0 {
			b.WriteString("]\n")
		} else {
			b.WriteString("\n")
			for _, p := range s.Params {
				b.WriteString("\t\t\t{ ")
				fmt.Fprintf(&b, "name: %q, required: %t", p.Name, p.Required)
				if len(p.Enum) > 0 {
					b.WriteString(", enum: [")
					for i, e := range p.Enum {
						if i > 0 {
							b.WriteString(", ")
						}
						fmt.Fprintf(&b, "%q", e)
					}
					b.WriteString("]")
				}
				if p.Default != "" {
					fmt.Fprintf(&b, ", default: %q", p.Default)
				}
				fmt.Fprintf(&b, ", desc: %q", p.Desc)
				b.WriteString(" },\n")
			}
			b.WriteString("\t\t]\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("];\n\n")

	// 조회 헬퍼 — 팔레트/노드가 공통으로 쓴다.
	b.WriteString(`export const STEP_CONTRACT_BY_TYPE: Record<string, StepContract> = Object.fromEntries(
	STEP_CONTRACTS.map((c) => [c.type, c])
);

// tailwind 색상 계열 → 실제 클래스. tailwind 는 동적 문자열을 purge 하므로
// 클래스 전체를 리터럴로 나열해야 한다.
export const STEP_TYPE_COLORS: Record<string, { bg: string; text: string }> = {
`)
	for _, s := range scenario.Specs {
		fmt.Fprintf(&b, "\t%s: { bg: 'bg-%s-100', text: 'text-%s-700' },\n", s.Type, s.Color, s.Color)
	}
	b.WriteString("};\n")

	out := filepath.Join("ui", "src", "routes", "agent", "scenario-canvas", "step-contract.ts")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "쓰기 실패:", err)
		os.Exit(1)
	}
	fmt.Println("생성됨:", out, "-", len(scenario.Specs), "step types")
}
