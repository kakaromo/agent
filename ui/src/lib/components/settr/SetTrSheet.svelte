<script lang="ts">
	import * as Sheet from '$lib/components/ui/sheet/index.js';
	import { DataTable, DateCell } from '$lib/components/data-table';
	import { renderComponent } from '$lib/components/ui/data-table/render-helpers.js';
	import type { ColumnDef } from '@tanstack/table-core';
	import LoaderCircle from '@lucide/svelte/icons/loader-circle';
	import { sendHeadCommand } from '$lib/api/testdb.js';
	import type {
		SlotInfomation,
		CompatibilityTestRequest,
		PerformanceTestRequest
	} from '$lib/api/types.js';
	import type { HeadSlotData } from '$lib/api/headSlotStore.svelte.js';

	type TrItem = CompatibilityTestRequest | PerformanceTestRequest;

	interface SlotItem {
		slot: SlotInfomation;
		headData?: HeadSlotData;
	}

	interface Props {
		open: boolean;
		activeTab: string;
		currentTRs: TrItem[];
		selectedIds: Set<number>;
		currentItems: SlotItem[];
		onApplied?: () => void | Promise<void>;
	}

	let {
		open = $bindable(),
		activeTab,
		currentTRs,
		selectedIds,
		currentItems,
		onApplied
	}: Props = $props();

	let pickedTrId = $state<number | null>(null);
	let commandBusy = $state(false);
	let trSearchQuery = $state('');

	export function openSetTR() {
		pickedTrId = null;
		commandBusy = false;
		trSearchQuery = '';
		open = true;
	}

	interface TrRow {
		id: number;
		fw: string;
		date: string;
	}

	const trTableData = $derived<TrRow[]>(
		currentTRs
			.map((tr) => ({
				id: tr.id,
				fw: tr.fw ?? '',
				date: tr.date ?? ''
			}))
			.filter((row) => {
				if (!trSearchQuery.trim()) return true;
				const q = trSearchQuery.trim().toLowerCase();
				return (
					String(row.id).includes(q) ||
					row.fw.toLowerCase().includes(q) ||
					row.date.toLowerCase().includes(q)
				);
			})
	);

	const trTableColumns: ColumnDef<TrRow, unknown>[] = [
		{ accessorKey: 'id', header: 'ID', enableSorting: true },
		{ accessorKey: 'fw', header: 'FW', enableSorting: true },
		{
			accessorKey: 'date',
			header: 'Date',
			enableSorting: true,
			cell: ({ row }) => renderComponent(DateCell, { date: row.original.date })
		}
	];

	function getSelectedSlotNumbers(): number[] {
		return currentItems
			.filter((item) => selectedIds.has(item.slot.id))
			.map((item) => item.headData?.slotIndex ?? item.slot.slotNumber ?? 0);
	}

	async function applySetTR() {
		if (pickedTrId == null) return;
		const slotNumbers = getSelectedSlotNumbers();
		if (slotNumbers.length === 0) return;

		commandBusy = true;
		try {
			await sendHeadCommand({
				source: activeTab,
				command: 'settr',
				slotNumbers,
				data: String(pickedTrId)
			});
		} catch {
			// errors surfaced via backend; caller sheet just closes
		} finally {
			commandBusy = false;
			open = false;
			await onApplied?.();
		}
	}
</script>

<Sheet.Root bind:open>
	<Sheet.Content
		side="right"
		class="w-screen flex flex-col max-h-[100dvh]"
		onInteractOutside={(e) => e.preventDefault()}
		onFocusOutside={(e) => e.preventDefault()}
	>
		<Sheet.Header>
			<Sheet.Title>Set Test Request</Sheet.Title>
			<Sheet.Description>
				Applying to {selectedIds.size} slot(s) on <strong>{activeTab}</strong>
			</Sheet.Description>
		</Sheet.Header>

		<div class="flex-1 overflow-y-auto py-3 space-y-2">
			<div class="flex items-center gap-2">
				<input
					type="text"
					class="h-6 w-64 px-2 text-[10px] rounded-md border border-border bg-background placeholder:text-muted-foreground"
					placeholder="Search TR by ID, FW, or Date..."
					bind:value={trSearchQuery}
				/>
				<span class="text-[10px] text-muted-foreground">{trTableData.length} / {currentTRs.length}</span>
			</div>
			<DataTable
				data={trTableData}
				columns={trTableColumns}
				enableRowSelection={true}
				enableMultiRowSelection={false}
				enableColumnVisibility={false}
				showPagination={false}
				compact={true}
				initialPageSize={9999}
				initialSorting={[{ id: 'id', desc: true }]}
				getRowId={(row) => String(row.id)}
				onSelectionChange={(rows) => {
					pickedTrId = rows.length > 0 ? rows[0].id : null;
				}}
			/>
		</div>

		<Sheet.Footer>
			<div class="flex items-center gap-2 w-full">
				<div class="ml-auto flex gap-2">
					<button
						class="rounded-md border px-3 py-1.5 text-[10px] hover:bg-muted transition-colors"
						onclick={() => (open = false)}
					>
						Cancel
					</button>
					<button
						class="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-[10px] font-medium text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-50"
						disabled={commandBusy || pickedTrId == null}
						onclick={applySetTR}
					>
						{#if commandBusy}
							<LoaderCircle class="size-3 animate-spin" />
							Sending...
						{:else}
							Apply
						{/if}
					</button>
				</div>
			</div>
		</Sheet.Footer>
	</Sheet.Content>
</Sheet.Root>
