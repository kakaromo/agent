<script lang="ts">
	import { type ColumnDef } from '@tanstack/table-core';
	import { emptyState } from '$lib/styles/common.js';
	import { DataTable } from '$lib/components/data-table';
	import * as Card from '$lib/components/ui/card';
	import SectionHeader from './SectionHeader.svelte';
	import { btnBase, btnActive, btnInactive, groupClass } from './perfStyles';

	interface LatencyStats {
		min: number;
		max: number;
		avg: number;
		med: number;
		std: number;
		'99th': number;
		'99.9th': number;
		'99.99th': number;
		'99.9999th': number;
		total: number;
		[bucket: string]: number | null;
	}

	interface CycleEntry {
		cycle: number;
		Write?: { dirty?: LatencyStats | null; sustain?: LatencyStats | null; total?: LatencyStats | null };
		Read?: { dirty?: LatencyStats | null; sustain?: LatencyStats | null; total?: LatencyStats | null };
		Unmap?: { dirty?: LatencyStats | null; sustain?: LatencyStats | null; total?: LatencyStats | null };
	}

	const OPERATIONS = ['Write', 'Read', 'Unmap'] as const;
	const PHASES = ['dirty', 'sustain', 'total'] as const;
	const STAT_KEYS = ['min', 'max', 'avg', 'med', 'std', '99th', '99.9th', '99.99th', '99.9999th', 'total'] as const;
	const BUCKET_ORDER = [
		'< 1ms', '< 10ms', '< 50ms', '< 100ms', '< 300ms',
		'< 500ms', '< 1s', '< 5s', '< 10s', '10s <='
	] as const;

	interface Props {
		data: CycleEntry[];
		tcName: string;
		fw?: string;
	}

	let { data, tcName, fw }: Props = $props();

	let activeOp = $state('');
	let activePhase = $state('');

	// Available operations (exist in at least one cycle)
	const availableOps = $derived(
		OPERATIONS.filter((op) => data.some((c) => c[op] != null))
	);

	// Available phases for current operation
	const availablePhases = $derived(
		PHASES.filter((ph) =>
			data.some((c) => {
				const opData = c[activeOp as keyof CycleEntry] as Record<string, unknown> | undefined;
				return opData?.[ph] != null;
			})
		)
	);

	// Auto-select operation
	$effect(() => {
		if (!availableOps.includes(activeOp as any)) {
			activeOp = availableOps[0] ?? '';
		}
	});

	// Auto-select phase when operation changes
	$effect(() => {
		if (!availablePhases.includes(activePhase as any)) {
			activePhase = availablePhases[0] ?? '';
		}
	});

	// Current stats per cycle
	const currentStats = $derived(
		data
			.map((c) => {
				const opData = c[activeOp as keyof CycleEntry] as Record<string, LatencyStats | null> | undefined;
				const stats = opData?.[activePhase];
				if (!stats) return null;
				return { cycle: c.cycle, ...stats };
			})
			.filter((s): s is NonNullable<typeof s> => s != null)
	);

	// Stats table columns
	const statsColumns: ColumnDef<(typeof currentStats)[number], unknown>[] = $derived([
		{ accessorKey: 'cycle', header: 'Cycle', cell: ({ row }) => `Cycle ${row.original.cycle}` },
		...STAT_KEYS.filter((k) => k !== 'total').map((key) => ({
			accessorKey: key,
			header: key,
			cell: ({ row }: { row: any }) => {
				const v = row.original[key];
				return v != null ? v.toFixed(3) : '—';
			}
		})),
		{
			accessorKey: 'total',
			header: 'total',
			cell: ({ row }: { row: any }) => {
				const v = row.original.total;
				return v != null ? v.toLocaleString() : '—';
			}
		}
	]);

	// Distribution table: rows = buckets, columns = cycles
	const distRows = $derived(
		BUCKET_ORDER.map((bucket) => {
			const row: Record<string, string | number> = { bucket };
			for (const s of currentStats) {
				row[`c${s.cycle}`] = (s as any)[bucket] ?? 0;
			}
			return row;
		})
	);

	const distColumns: ColumnDef<(typeof distRows)[number], unknown>[] = $derived([
		{ accessorKey: 'bucket', header: 'Latency Range' },
		...currentStats.map((s) => ({
			accessorKey: `c${s.cycle}`,
			header: `Cycle ${s.cycle}`,
			cell: ({ row }: { row: any }) => {
				const v = row.original[`c${s.cycle}`];
				return v != null ? Number(v).toLocaleString() : '0';
			}
		}))
	]);


</script>

<div class="space-y-3">
	<!-- Toolbar -->
	<div class="flex items-center gap-1.5 flex-wrap">
		{#if availableOps.length > 1}
			<div class={groupClass}>
				{#each availableOps as op (op)}
					<button
						class="{btnBase} {activeOp === op ? btnActive : btnInactive}"
						onclick={() => (activeOp = op)}
					>
						{op}
					</button>
				{/each}
			</div>
			<div class="w-px h-5 bg-border"></div>
		{/if}

		{#if availablePhases.length > 1}
			<div class={groupClass}>
				{#each availablePhases as ph (ph)}
					<button
						class="{btnBase} {activePhase === ph ? btnActive : btnInactive}"
						onclick={() => (activePhase = ph)}
					>
						{ph}
					</button>
				{/each}
			</div>
		{/if}

	</div>

	{#if currentStats.length === 0}
		<div class="{emptyState}">
			<span class="text-sm">No latency data for "{activeOp} / {activePhase}"</span>
			<span class="text-[11px] text-muted-foreground/60">Try selecting a different operation or phase above.</span>
		</div>
	{:else}
		<!-- Stats Table -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Statistics — {activeOp} / {activePhase}" />
			<Card.Content class="p-2">
				<DataTable
					data={currentStats}
					columns={statsColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={(row) => String(row.cycle)}
				/>
			</Card.Content>
		</Card.Root>

		<!-- Latency Table -->
		<Card.Root class="gap-0 p-0 overflow-hidden">
			<SectionHeader title="Latency — {activeOp} / {activePhase}" />
			<Card.Content class="p-2">
				<DataTable
					data={distRows}
					columns={distColumns}
					showPagination={false}
					compact={true}
					enableColumnVisibility={false}
					enableCellCopy={true}
					getRowId={(row) => String(row.bucket)}
				/>
			</Card.Content>
		</Card.Root>
	{/if}
</div>
