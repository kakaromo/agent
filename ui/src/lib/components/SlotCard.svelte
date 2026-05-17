<script lang="ts">
	import type { SlotInfomation } from '$lib/api/types.js';
	import type { HeadSlotData } from '$lib/api/headSlotStore.svelte.js';
	import { resolveSlotIcon, resolveSlotGradient, getTextClass } from '$lib/config/slotState.js';
	import { slotHover } from '$lib/stores/slotHoverStore.svelte.js';
	import ZapIcon from '@lucide/svelte/icons/zap';
	import StickyNoteIcon from '@lucide/svelte/icons/sticky-note';
	import DatabaseIcon from '@lucide/svelte/icons/database';

	interface Props {
		slot: SlotInfomation;
		headData?: HeadSlotData;
		selected?: boolean;
		compact?: boolean;
		hasMemo?: boolean;
		hasSlotPreCmd?: boolean;
		hasTcPreCmd?: boolean;
		hasPreCommand?: boolean;
		preCommandName?: string;
		metaMonitoring?: boolean;
		onclick?: (e: MouseEvent) => void;
		oncontextmenu?: (e: MouseEvent) => void;
	}

	let { slot, headData, selected = false, compact = false, hasMemo = false, hasSlotPreCmd = false, hasTcPreCmd = false, hasPreCommand = false, preCommandName, metaMonitoring = false, onclick, oncontextmenu }: Props = $props();

	// Connection: 0=not connected, 1=connected, 2=upload possible
	const CONNECTION_DOT: Record<number, string> = { 0: 'bg-gray-400', 1: 'bg-emerald-400', 2: 'bg-blue-400' };

	function onMouseEnter() {
		slotHover.set({
			slot,
			headData,
			hasMemo,
			hasSlotPreCmd,
			hasTcPreCmd,
			hasPreCommand,
			preCommandName,
			metaMonitoring
		});
	}

	function onMouseLeave() {
		slotHover.clearSoon();
	}

	const iconInfo = $derived.by(() => {
		const info = resolveSlotIcon(headData?.testState ?? '');
		return { ...info, textClass: getTextClass(info.color) };
	});

	const faIcon = $derived(iconInfo.fa.icon);
	const faViewBox = $derived(`0 0 ${faIcon[0]} ${faIcon[1]}`);
	const faPath = $derived(faIcon[4] as string);

	const gradientClass = $derived(resolveSlotGradient(headData?.testState ?? ''));

	// TC name — show when state has a TC assigned
	const TC_HIDDEN_STATES = new Set(['none', 'standby', '']);
	const showTc = $derived(
		!!headData?.testToolName &&
			!TC_HIDDEN_STATES.has((headData.testState ?? '').toLowerCase().trim())
	);

	// Watermark only for active states
	const WATERMARK_KEYWORDS = ['running', 'pass', 'fail', 'stop', 'warning', 'booting', 'onedown', 'running ffu', 'provisioning', 'downloading', 'getting info'];
	const showWatermark = $derived.by(() => {
		const ts = (headData?.testState ?? '').toLowerCase().trim();
		return WATERMARK_KEYWORDS.some((k) => ts.includes(k));
	});

</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
	class="slot-card relative rounded-lg border cursor-pointer select-none transition-all duration-200 hover:shadow-md hover:border-primary/40 w-[170px] h-[150px] overflow-hidden {gradientClass} {selected ? 'ring-[3px] ring-primary border-primary shadow-lg' : 'border-border/50'} {iconInfo.animate && !selected ? 'slot-active' : ''}"
	{onclick}
	{oncontextmenu}
	onmouseenter={onMouseEnter}
	onmouseleave={onMouseLeave}
>
	<!-- Header -->
	<div class="relative z-10 flex items-center justify-between px-2.5 py-1.5 bg-gray-900">
		<div class="flex items-baseline gap-1.5 min-w-0">
			<span class="font-bold text-white text-xs whitespace-nowrap">
				Slot {headData?.slotIndex ?? slot.slotNumber ?? '—'}
			</span>
			{#if headData?.setLocation || slot.tentacleName}
				<span class="font-bold text-white text-xs truncate"
					>({(headData?.setLocation || slot.tentacleName || '').toUpperCase()})</span
				>
			{/if}
		</div>
		<div class="flex items-center gap-1 shrink-0">
			{#if hasSlotPreCmd}
				<ZapIcon class="size-3 text-amber-400" />
			{/if}
			{#if hasTcPreCmd}
				<ZapIcon class="size-3 text-purple-400" />
			{:else if hasPreCommand && !hasSlotPreCmd}
				<ZapIcon class="size-3 text-amber-400" />
			{/if}
			{#if hasMemo}
				<StickyNoteIcon class="size-3 text-sky-400" />
			{/if}
			{#if metaMonitoring}
				<DatabaseIcon class="size-3 text-purple-300" />
			{/if}
			{#if headData}
				<span class="size-2.5 rounded-full shrink-0 {CONNECTION_DOT[headData.connection] ?? 'bg-gray-400'}"></span>
			{/if}
		</div>
	</div>

	<!-- Content -->
	<div class="relative z-10 px-2.5 pt-1.5 pb-2 space-y-px">
		{#if headData}
			<!-- Status — 다른 보조 정보 줄과 동일하게 block + truncate 로 좌측 시작 위치 통일 -->
			<div class="font-bold text-sm {iconInfo.textClass} leading-tight truncate mb-0.5">
				{headData.testState || 'Unknown'}{#if iconInfo.animate}<span class="slot-breathing-dot inline-block ml-1.5 size-1.5 rounded-full align-middle {iconInfo.textClass.replace('text-', 'bg-')}"></span>{/if}
			</div>

			<!-- 보조 정보 -->
			{#if headData.board}
				<div class="text-[11px] text-muted-foreground/80 truncate leading-tight">{headData.board}</div>
			{:else if headData.setModelName}
				<div class="text-[11px] text-muted-foreground/80 truncate leading-tight">{headData.setModelName}</div>
			{/if}
			{#if headData.product}
				<div class="text-[11px] text-muted-foreground/80 truncate leading-tight">{headData.product}</div>
			{:else if headData.controller}
				<div class="text-[11px] text-muted-foreground/80 truncate leading-tight">{headData.controller}</div>
			{/if}
			{#if headData.fwVer}
				<div class="text-[11px] text-muted-foreground/80 truncate leading-tight">{headData.fwVer}</div>
			{/if}
			{#if headData.fileSystem && headData.freeArea}
				<div class="text-[11px] text-muted-foreground/80 truncate leading-tight">{headData.fileSystem} · {headData.freeArea}</div>
			{:else if headData.freeArea}
				<div class="text-[11px] text-muted-foreground/80 leading-tight">{headData.freeArea}</div>
			{:else if headData.fileSystem}
				<div class="text-[11px] text-muted-foreground/80 truncate leading-tight">{headData.fileSystem}</div>
			{/if}
			{#if showTc}
				<div class="text-[11px] text-foreground/70 font-medium truncate leading-tight">{headData.testToolName}</div>
			{/if}
			{#if headData.runningTime && headData.runningTime !== '0h 00m 00s'}
				<div class="text-[11px] text-muted-foreground/80 tabular-nums leading-tight">{headData.runningTime}</div>
			{/if}
		{:else}
			<div class="text-xs text-gray-400 pt-1">Not Connected</div>
		{/if}
	</div>

	<!-- Watermark icon (only for active states) -->
	{#if showWatermark}
		<div class="absolute bottom-1 right-1.5 z-0 opacity-[0.06] pointer-events-none {iconInfo.animate ? 'slot-spin-slow' : ''}">
			<svg class="size-14 fill-current {iconInfo.textClass}" viewBox={faViewBox} xmlns="http://www.w3.org/2000/svg">
				<path d={faPath} />
			</svg>
		</div>
	{/if}

	<!-- Running shimmer bar -->
	{#if iconInfo.animate}
		<div class="absolute bottom-0 left-0 right-0 h-[3px] z-20 overflow-hidden rounded-b-lg">
			<div class="slot-shimmer h-full {iconInfo.textClass.replace('text-', 'bg-')} opacity-60"></div>
		</div>
	{/if}
</div>

<style>
	/* 활성 상태 카드 — 미세한 컬러 border + glow */
	:global(.slot-active) {
		border-color: rgba(52, 211, 153, 0.35) !important;
		box-shadow: 0 0 0 1px rgba(52, 211, 153, 0.12), 0 0 12px -3px rgba(52, 211, 153, 0.15);
	}

	/* 상태 텍스트 옆 breathing dot — 3초 주기 부드러운 투명도 전환 */
	:global(.slot-breathing-dot) {
		animation: slot-breathe 3s ease-in-out infinite;
	}

	@keyframes slot-breathe {
		0%, 100% { opacity: 0.4; transform: scale(0.85); }
		50% { opacity: 1; transform: scale(1); }
	}

	/* 워터마크 느린 회전 — 12초 주기 */
	:global(.slot-spin-slow) {
		animation: spin 12s linear infinite;
	}

	/* 하단 shimmer bar — 좌→우 빛 흐름 */
	:global(.slot-shimmer) {
		background: linear-gradient(90deg, transparent 0%, currentColor 50%, transparent 100%);
		background-size: 200% 100%;
		animation: slot-shimmer 3s ease-in-out infinite;
	}

	@keyframes slot-shimmer {
		0% { background-position: 200% 0; }
		100% { background-position: -200% 0; }
	}
</style>
