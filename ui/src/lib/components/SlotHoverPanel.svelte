<script lang="ts">
	import { slotHover } from '$lib/stores/slotHoverStore.svelte.js';

	const CONNECTION_LABEL: Record<number, string> = {
		0: 'Not Connected',
		1: 'Connected',
		2: 'Upload'
	};

	const info = $derived(slotHover.current);

	const rows = $derived.by(() => {
		const i = info;
		if (!i) return [] as [string, string][];
		const out: [string, string][] = [];
		const add = (label: string, value: string | undefined | null) => {
			if (value && String(value).trim()) out.push([label, String(value)]);
		};
		const h = i.headData;
		if (h) {
			add('State', h.testState);
			add('Connection', CONNECTION_LABEL[h.connection]);
			add('Product', h.product);
			add('Board', h.board);
			add('Controller', h.controller);
			add('FW', h.fwVer);
			add('FW Date', h.fwDate);
			add('Model', h.setModelName);
			add('Location', h.setLocation);
			add('Battery', h.remainBattery);
			add('FreeArea', h.freeArea);
			add('FileSystem', h.fileSystem);
			add('TC', h.testToolName);
			add('TR', h.testTrName);
			add('Running', h.runningTime);
			add('RunState', h.runningState);
			add('USB', h.usbId);
		}
		if (i.hasSlotPreCmd && i.preCommandName) add('Slot Pre-Cmd', i.preCommandName);
		else if (i.hasTcPreCmd) add('TC Pre-Cmd', 'Set');
		else if (i.hasPreCommand) add('Pre-Cmd', i.preCommandName ?? '');
		if (i.metaMonitoring) add('Meta', 'Monitoring');
		return out;
	});

	const headerTitle = $derived.by(() => {
		const i = info;
		if (!i) return '';
		const idx = i.headData?.slotIndex ?? i.slot.slotNumber ?? '—';
		const src = i.headData?.source ?? i.slot.tentacleName ?? '';
		return src ? `Slot ${idx} — ${src}` : `Slot ${idx}`;
	});
</script>

{#if info && rows.length > 0}
	<!-- 우하단 고정 — 슬롯 그리드와 마우스 동선을 가리지 않도록 화면 코너에 배치 -->
	<div
		class="fixed bottom-3 right-3 z-40 pointer-events-none animate-in fade-in-0 slide-in-from-bottom-2 duration-150"
	>
		<div
			class="rounded-lg border bg-popover text-popover-foreground shadow-lg px-3 py-2 text-xs w-[260px] max-h-[60vh] overflow-y-auto pointer-events-auto"
		>
			<div class="font-semibold text-[11px] mb-1 pb-1 border-b border-border/50 truncate">
				{headerTitle}
			</div>
			{#each rows as [label, value]}
				<div class="flex gap-2">
					<span class="text-muted-foreground shrink-0 w-[72px]">{label}</span>
					<span class="font-medium truncate flex-1">{value}</span>
				</div>
			{/each}
		</div>
	</div>
{/if}
