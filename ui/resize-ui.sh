#!/bin/bash
#
# Portal UI Size Preset Changer
# Usage: ./resize-ui.sh [compact|default|large]
#
# compact : 가장 작은 크기 (기본 상태)
# default : 한 단계 업 (일반적인 웹 크기)
# large   : 두 단계 업 (넉넉한 크기)
#

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$SCRIPT_DIR/src"

# ─── Color output ───
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${CYAN}[INFO]${NC} $1"; }
ok()    { echo -e "${GREEN}  ✓${NC} $1"; }
warn()  { echo -e "${YELLOW}  ⚠${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# ─── Replacement engine ───
# replace_exact <file> <old_literal> <new_literal>
# Global replacement across the entire file. Uses perl \Q..\E for literal matching.
# The (?![.\d]) lookahead prevents partial matches (e.g., "size-3" matching in "size-3.5").
replace_exact() {
    local file="$1" old="$2" new="$3"
    [ "$old" = "$new" ] && return 0
    perl -pi -e "s#\Q${old}\E(?![.\d])#${new}#g" "$file"
}

# replace_line <file> <context> <old> <new>
# Replace $old with $new only on lines containing $context.
replace_line() {
    local file="$1" context="$2" old="$3" new="$4"
    [ "$old" = "$new" ] && return 0
    perl -pi -e "if (index(\$_, q#${context}#) >= 0) { s#\Q${old}\E(?![.\d])#${new}#g }" "$file"
}

# ─── Preset values ───
# Index: 0=compact, 1=default, 2=large
preset_index() {
    case "$1" in
        compact) echo 0 ;;
        default) echo 1 ;;
        large)   echo 2 ;;
    esac
}

# Helper to pick value by index
pick() {
    local i="$1"; shift
    local arr=("$@")
    echo "${arr[$i]}"
}

# ─── Apply a size class swap across ALL presets for a file ───
# swap <file> <value0> <value1> <value2> <idx>
# Replaces whichever of value0/value1/value2 is currently in the file with the target value.
swap() {
    local file="$1" v0="$2" v1="$3" v2="$4" idx="$5"
    local target
    target=$(pick "$idx" "$v0" "$v1" "$v2")
    # Replace all three possible current values with target
    replace_exact "$file" "$v0" "$target"
    replace_exact "$file" "$v1" "$target"
    replace_exact "$file" "$v2" "$target"
}

# swap_ctx <file> <context> <value0> <value1> <value2> <idx>
# Same as swap but only on lines containing context.
swap_ctx() {
    local file="$1" ctx="$2" v0="$3" v1="$4" v2="$5" idx="$6"
    local target
    target=$(pick "$idx" "$v0" "$v1" "$v2")
    replace_line "$file" "$ctx" "$v0" "$target"
    replace_line "$file" "$ctx" "$v1" "$target"
    replace_line "$file" "$ctx" "$v2" "$target"
}

apply_preset() {
    local idx="$1"

    info "Applying changes...\n"

    # ════════════════════════════════════════════
    # app.css — body text
    # ════════════════════════════════════════════
    local f="$SRC/app.css"
    swap_ctx "$f" "bg-background text-foreground" "text-sm" "text-base" "text-lg" "$idx"
    ok "app.css"

    # ════════════════════════════════════════════
    # +layout.svelte — header
    # ════════════════════════════════════════════
    f="$SRC/routes/+layout.svelte"
    # Header bar height + padding (context: "items-center" on header div)
    swap_ctx "$f" "flex h-" "h-10" "h-12" "h-14" "$idx"
    swap_ctx "$f" "items-center px-" "px-4 gap-6" "px-5 gap-6" "px-6 gap-6" "$idx"
    # Logo text (context: "font-semibold" — only the logo line)
    swap_ctx "$f" "font-semibold" "text-sm" "text-base" "text-lg" "$idx"
    # Nav link text size (context: "inline-flex items-center gap-0.5")
    swap_ctx "$f" "gap-0.5" "text-[11px]" "text-xs" "text-sm" "$idx"
    # Nav link padding (context: "gap-0.5" — nav link line)
    swap_ctx "$f" "gap-0.5" "px-2 py-1" "px-2.5 py-1.5" "px-3 py-2" "$idx"
    # Nav icon size (context: "item.icon")
    swap_ctx "$f" "item.icon" "size-2.5" "size-3.5" "size-4" "$idx"
    # Auth username text
    swap_ctx "$f" "auth.name" "text-xs" "text-sm" "text-base" "$idx"
    # Logout button text & icon
    swap_ctx "$f" "Logout" "text-[11px]" "text-xs" "text-sm" "$idx"
    swap_ctx "$f" "LogOutIcon" "size-3" "size-3.5" "size-4" "$idx"
    # Main content padding
    swap_ctx "$f" "bg-white dark:bg-background" "p-4" "p-5" "p-6" "$idx"
    ok "+layout.svelte"

    # ════════════════════════════════════════════
    # DataTable.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/DataTable.svelte"
    # Table body text: <table class="w-full text-[11px]">
    swap_ctx "$f" "w-full" "text-[11px]" "text-xs" "text-sm" "$idx"
    # <th> row height + padding + text
    swap_ctx "$f" "text-left" "h-7" "h-8" "h-9" "$idx"
    swap_ctx "$f" "text-left" "px-2" "px-3" "px-4" "$idx"
    swap_ctx "$f" "text-left" "text-[11px]" "text-xs" "text-sm" "$idx"
    # <td> row height + padding
    swap_ctx "$f" "whitespace-nowrap" "h-7" "h-8" "h-9" "$idx"
    swap_ctx "$f" "whitespace-nowrap" "px-2" "px-3" "px-4" "$idx"
    # group row td with colspan
    swap_ctx "$f" "colspan" "h-7" "h-8" "h-9" "$idx"
    # sort icons
    swap_ctx "$f" "Arrow" "size-2.5" "size-3" "size-3.5" "$idx"
    # group-by label text
    swap_ctx "$f" "Group:" "text-[10px]" "text-xs" "text-sm" "$idx"
    # group-by button strip text
    swap_ctx "$f" "flex rounded border" "text-[10px]" "text-xs" "text-sm" "$idx"
    # group-by buttons padding
    swap_ctx "$f" "transition-colors" "py-0.5" "py-1" "py-1.5" "$idx"
    # group row inner div text
    swap_ctx "$f" "font-medium" "text-[11px]" "text-xs" "text-sm" "$idx"
    # Chevron icons
    swap_ctx "$f" "Chevron" "size-3" "size-3.5" "size-4" "$idx"
    # expand toggle button size
    swap_ctx "$f" "justify-center" "size-5" "size-6" "size-7" "$idx"
    ok "DataTable.svelte"

    # ════════════════════════════════════════════
    # DataTableToolbar.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/DataTableToolbar.svelte"
    # search input text + width
    swap_ctx "$f" "rounded border" "text-[10px]" "text-xs" "text-sm" "$idx"
    swap_ctx "$f" "bg-background focus" "w-36" "w-44" "w-52" "$idx"
    # search input padding (context: "pl-5 pr-2")
    swap_ctx "$f" "pl-5 pr-2" "py-0.5" "py-1" "py-1.5" "$idx"
    # action button padding (context: "gap-0.5 px")
    swap_ctx "$f" "gap-0.5 px" "py-0.5" "py-1" "py-1.5" "$idx"
    # Search icon
    swap_ctx "$f" "Search" "size-2.5" "size-3" "size-3.5" "$idx"
    # Action button icons
    swap_ctx "$f" "Icon" "size-2.5" "size-3" "size-3.5" "$idx"
    ok "DataTableToolbar.svelte"

    # ════════════════════════════════════════════
    # DataTablePagination.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/DataTablePagination.svelte"
    swap_ctx "$f" "Select.Trigger" "h-7" "h-8" "h-9" "$idx"
    swap_ctx "$f" "Button" "size-7" "size-8" "size-9" "$idx"
    swap_ctx "$f" "Chevron" "size-3.5" "size-4" "size-4.5" "$idx"
    ok "DataTablePagination.svelte"

    # ════════════════════════════════════════════
    # DataTableColumnToggle.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/DataTableColumnToggle.svelte"
    swap_ctx "$f" "rounded border" "text-[10px]" "text-xs" "text-sm" "$idx"
    swap_ctx "$f" "rounded border" "py-0.5" "py-1" "py-1.5" "$idx"
    swap_ctx "$f" "Columns3" "size-2.5" "size-3" "size-3.5" "$idx"
    ok "DataTableColumnToggle.svelte"

    # ════════════════════════════════════════════
    # CopyCell.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/cells/CopyCell.svelte"
    if [ -f "$f" ]; then
        swap "$f" "size-3" "size-3.5" "size-4" "$idx"
        ok "CopyCell.svelte"
    fi

    # ════════════════════════════════════════════
    # ResultCell.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/cells/ResultCell.svelte"
    swap_ctx "$f" "font-medium" "text-[10px]" "text-xs" "text-sm" "$idx"
    swap_ctx "$f" "font-medium" "px-1.5" "px-2" "px-2.5" "$idx"
    swap "$f" "size-2.5" "size-3" "size-3.5" "$idx"
    ok "ResultCell.svelte"

    # ════════════════════════════════════════════
    # StatusCell.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/cells/StatusCell.svelte"
    swap_ctx "$f" "rounded-full" "size-2" "size-2.5" "size-3" "$idx"
    ok "StatusCell.svelte"

    # ════════════════════════════════════════════
    # SelectCell.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/data-table/cells/SelectCell.svelte"
    swap_ctx "$f" "translate-y" "size-4.5" "size-5" "size-5.5" "$idx"
    ok "SelectCell.svelte"

    # ════════════════════════════════════════════
    # SlotCard.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/SlotCard.svelte"
    swap_ctx "$f" "cursor-pointer" "w-[160px]" "w-[190px]" "w-[220px]" "$idx"
    swap_ctx "$f" "cursor-pointer" "h-[140px]" "h-[160px]" "h-[180px]" "$idx"
    ok "SlotCard.svelte"

    # ════════════════════════════════════════════
    # tabs-list.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/ui/tabs/tabs-list.svelte"
    swap_ctx "$f" "bg-muted" "h-8" "h-9" "h-10" "$idx"
    # Tab list padding: use unique enough context
    local tl_p; tl_p=$(pick "$idx" "p-[3px]" "p-1" "p-1.5")
    replace_line "$f" "rounded-lg" "p-1.5" "$tl_p"
    replace_line "$f" "rounded-lg" "p-[3px]" "$tl_p"
    # p-1 (exact, not p-1.5 or p-[3px])
    perl -pi -e "if (index(\$_, 'rounded-lg') >= 0) { s#p-1(?!\\.\\d)(?!\\[)#${tl_p}#g }" "$f"
    ok "tabs-list.svelte"

    # ════════════════════════════════════════════
    # tabs-trigger.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/ui/tabs/tabs-trigger.svelte"
    swap_ctx "$f" "font-medium" "text-xs" "text-sm" "text-base" "$idx"
    swap_ctx "$f" "font-medium" "px-2" "px-3" "px-4" "$idx"
    ok "tabs-trigger.svelte"

    # ════════════════════════════════════════════
    # perf-content — btnBase toolbar pattern (all 15 components)
    # ════════════════════════════════════════════
    local perf_dir="$SRC/lib/components/perf-content"
    for pf in "$perf_dir"/*.svelte; do
        [ -f "$pf" ] || continue
        local basename; basename=$(basename "$pf")
        # btnBase constant: 'px-2.5 py-1 text-[11px] transition-colors'
        swap_ctx "$pf" "btnBase" "text-[11px]" "text-xs" "text-sm" "$idx"
        swap_ctx "$pf" "btnBase" "px-2.5 py-1" "px-3 py-1.5" "px-3.5 py-2" "$idx"
        # Card title text
        swap_ctx "$pf" "font-medium text-muted-foreground" "text-xs" "text-sm" "text-base" "$idx"
        # Hide/Show toggle button text
        swap_ctx "$pf" "showRawData" "text-[11px]" "text-xs" "text-sm" "$idx"
        # Download icon
        swap_ctx "$pf" "Download" "size-3" "size-3.5" "size-4" "$idx"
    done
    ok "perf-content/*.svelte"

    # ════════════════════════════════════════════
    # PerfRenderer.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/perf-content/PerfRenderer.svelte"
    if [ -f "$f" ]; then
        swap_ctx "$f" "yAxisMax" "text-xs" "text-sm" "text-base" "$idx"
        swap_ctx "$f" "yAxisMax" "px-2.5" "px-3" "px-3.5" "$idx"
        swap_ctx "$f" "yAxisMax" "size-2.5" "size-3" "size-3.5" "$idx"
    fi

    # ════════════════════════════════════════════
    # LogBrowserDialog.svelte
    # ════════════════════════════════════════════
    f="$SRC/lib/components/LogBrowserDialog.svelte"
    if [ -f "$f" ]; then
        swap_ctx "$f" "btnBase" "text-[11px]" "text-xs" "text-sm" "$idx"
        swap_ctx "$f" "font-medium text-muted-foreground" "text-xs" "text-sm" "text-base" "$idx"
        ok "LogBrowserDialog.svelte"
    fi

    # ════════════════════════════════════════════
    # bin-mapper components
    # ════════════════════════════════════════════
    for bmf in "$SRC/lib/components/bin-mapper"/*.svelte; do
        [ -f "$bmf" ] || continue
        local bmname; bmname=$(basename "$bmf")
        swap_ctx "$bmf" "btnBase" "text-[11px]" "text-xs" "text-sm" "$idx"
        swap_ctx "$bmf" "font-medium text-muted-foreground" "text-xs" "text-sm" "text-base" "$idx"
    done
    ok "bin-mapper/*.svelte"

    # ════════════════════════════════════════════
    # storage/+page.svelte
    # ════════════════════════════════════════════
    f="$SRC/routes/storage/+page.svelte"
    if [ -f "$f" ]; then
        # Bucket list & file browser text
        swap_ctx "$f" "Table.Head" "text-[11px]" "text-xs" "text-sm" "$idx"
        # Action buttons
        swap_ctx "$f" "h-6 text-" "text-[11px]" "text-xs" "text-sm" "$idx"
        ok "storage/+page.svelte"
    fi

    # ════════════════════════════════════════════
    # admin/+page.svelte — text-[10px] labels
    # ════════════════════════════════════════════
    f="$SRC/routes/admin/+page.svelte"
    if [ -f "$f" ]; then
        swap "$f" "text-[10px]" "text-xs" "text-sm" "$idx"
        ok "admin/+page.svelte"
    fi

    # ════════════════════════════════════════════
    # remote/+page.svelte
    # ════════════════════════════════════════════
    f="$SRC/routes/remote/+page.svelte"
    if [ -f "$f" ]; then
        swap_ctx "$f" "btnBase" "text-[11px]" "text-xs" "text-sm" "$idx"
        ok "remote/+page.svelte"
    fi

    # ════════════════════════════════════════════
    # slots/+page.svelte
    # ════════════════════════════════════════════
    f="$SRC/routes/testdb/slots/+page.svelte"
    # Full height calc (matches header height)
    swap_ctx "$f" "flex flex-col" "h-[calc(100vh-4rem)]" "h-[calc(100vh-4.5rem)]" "h-[calc(100vh-5rem)]" "$idx"
    # Small UI text in slot config
    swap "$f" "text-[10px]" "text-xs" "text-sm" "$idx"
    # Connection status badge
    swap_ctx "$f" "bg-card rounded border" "text-xs" "text-sm" "text-base" "$idx"
    # Apply/Cancel buttons
    swap_ctx "$f" "rounded-md border px-3" "text-sm" "text-base" "text-lg" "$idx"
    swap_ctx "$f" "rounded-md bg-primary px-3" "text-sm" "text-base" "text-lg" "$idx"
    swap_ctx "$f" "px-4 py-2" "text-sm" "text-base" "text-lg" "$idx"
    # Slot group header padding
    swap_ctx "$f" "border-b border-border" "py-3" "py-4" "py-5" "$idx"
    # Slot group badge
    swap_ctx "$f" "bg-muted rounded-lg" "text-xs" "text-sm" "text-base" "$idx"
    swap_ctx "$f" "bg-primary/10 rounded-lg" "text-xs" "text-sm" "text-base" "$idx"
    # Input heights
    swap_ctx "$f" "flex-1" "h-6" "h-7" "h-8" "$idx"
    ok "slots/+page.svelte"

    # ════════════════════════════════════════════
    # dashboard +page.svelte
    # ════════════════════════════════════════════
    f="$SRC/routes/+page.svelte"
    swap_ctx "$f" "rounded-md border px" "text-[11px]" "text-xs" "text-sm" "$idx"
    ok "+page.svelte (dashboard)"

    # ════════════════════════════════════════════
    # compatibility/+page.svelte
    # ════════════════════════════════════════════
    f="$SRC/routes/testdb/compatibility/+page.svelte"
    if [ -f "$f" ]; then
        swap_ctx "$f" "btnBase" "text-[11px]" "text-xs" "text-sm" "$idx"
        ok "compatibility/+page.svelte"
    fi

    # ════════════════════════════════════════════
    # performance/+page.svelte
    # ════════════════════════════════════════════
    f="$SRC/routes/testdb/performance/+page.svelte"
    if [ -f "$f" ]; then
        swap_ctx "$f" "btnBase" "text-[11px]" "text-xs" "text-sm" "$idx"
        ok "performance/+page.svelte"
    fi

    # ════════════════════════════════════════════
    # perf-generator/+page.svelte
    # ════════════════════════════════════════════
    f="$SRC/routes/devtools/perf-generator/+page.svelte"
    if [ -f "$f" ]; then
        swap_ctx "$f" "font-medium text-muted-foreground" "text-xs" "text-sm" "text-base" "$idx"
        ok "perf-generator/+page.svelte"
    fi

    echo ""
}

# ─── Show current state ───
show_status() {
    echo -e "\n${BOLD}=== Current UI Sizing ===${NC}\n"

    local f="$SRC/app.css"
    local body
    body=$(perl -ne 'print $1 if /bg-background.*?(text-(?:sm|base|lg))/' "$f")
    echo -e "  Body text:       ${CYAN}${body:-unknown}${NC}"

    f="$SRC/routes/+layout.svelte"
    local hdr
    hdr=$(perl -ne 'print $1 if /flex (h-\d+) items-center/' "$f" | head -1)
    echo -e "  Header height:   ${CYAN}${hdr:-unknown}${NC}"

    local logo
    logo=$(perl -ne 'print $1 if /(text-(?:sm|base|lg))\s+font-semibold/' "$f" | head -1)
    echo -e "  Logo text:       ${CYAN}${logo:-unknown}${NC}"

    local nav
    nav=$(perl -ne 'print $1 if /gap-0\.5.*?(text-(?:\[\d+px\]|xs|sm|base))/' "$f" | head -1)
    echo -e "  Nav text:        ${CYAN}${nav:-unknown}${NC}"

    f="$SRC/lib/components/data-table/DataTable.svelte"
    local tbl
    tbl=$(perl -ne 'print $1 if /w-full\s+(text-(?:\[[\dpx]+\]|xs|sm))/' "$f" | head -1)
    echo -e "  Table text:      ${CYAN}${tbl:-unknown}${NC}"

    local tbl_h
    tbl_h=$(perl -ne 'if (/(h-\d+)\s+px-\d/) { print $1; exit }' "$f")
    echo -e "  Table row:       ${CYAN}${tbl_h:-unknown}${NC}"

    f="$SRC/lib/components/SlotCard.svelte"
    local card
    card=$(perl -ne 'print $1 if /((?:min-w|w)-\[\d+px\])/' "$f" | head -1)
    echo -e "  SlotCard width:  ${CYAN}${card:-unknown}${NC}"

    local perf_btn
    perf_btn=$(perl -ne 'if (/btnBase.*?(text-(?:\[[\dpx]+\]|xs|sm))/) { print $1; exit }' "$SRC/lib/components/perf-content/GenPerf.svelte" 2>/dev/null)
    echo -e "  Perf toolbar:    ${CYAN}${perf_btn:-unknown}${NC}"

    echo ""
}

# ─── Main ───
main() {
    echo -e "\n${BOLD}╔══════════════════════════════════════╗${NC}"
    echo -e "${BOLD}║     Portal UI Size Preset Changer    ║${NC}"
    echo -e "${BOLD}╚══════════════════════════════════════╝${NC}\n"

    local preset="${1:-}"

    if [ -z "$preset" ]; then
        show_status
        echo -e "  ${BOLD}Presets:${NC}\n"
        echo -e "  ${GREEN}compact${NC}  가장 작은 크기 (기본 상태)"
        echo -e "           body: text-sm, header: h-10, table: text-[11px], row: h-7\n"
        echo -e "  ${GREEN}default${NC}  한 단계 업 (일반 웹 크기)"
        echo -e "           body: text-base, header: h-12, table: text-xs, row: h-8\n"
        echo -e "  ${GREEN}large${NC}    두 단계 업 (넉넉한 크기)"
        echo -e "           body: text-lg, header: h-14, table: text-sm, row: h-9\n"
        echo -e "  ${GREEN}status${NC}   현재 상태만 확인\n"
        echo -e "  Usage: ${CYAN}./resize-ui.sh [compact|default|large|status]${NC}\n"
        exit 0
    fi

    case "$preset" in
        status)
            show_status
            exit 0
            ;;
        compact|default|large)
            local idx
            idx=$(preset_index "$preset")
            echo -e "  Preset: ${GREEN}${preset}${NC}\n"
            apply_preset "$idx"
            ;;
        *)
            error "Unknown preset: $preset (use compact, default, large, or status)"
            ;;
    esac

    echo -e "${GREEN}${BOLD}Done!${NC} Run ${CYAN}npm run dev${NC} to see the changes.\n"
}

main "$@"
