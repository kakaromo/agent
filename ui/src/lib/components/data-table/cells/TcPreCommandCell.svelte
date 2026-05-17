<script lang="ts">
	import { listPreCommands, type PreCommand } from '$lib/api/preCommand.js';
	import { assignTc, unassignTc, getTcAssignments } from '$lib/api/preCommand.js';
	import XIcon from '@lucide/svelte/icons/x';
	import { toast } from 'svelte-sonner';

	interface Props {
		setLocation: string;
		tcPosition: number;
		tcState: string;
		onAssignmentChanged?: () => void;
	}

	let { setLocation, tcPosition, tcState, onAssignmentChanged }: Props = $props();

	let preCommands = $state<PreCommand[]>([]);
	let assignedId = $state<number | null>(null);
	let loading = $state(true);
	let saving = $state(false);

	const canEdit = $derived(tcState === 'NOTSTART');

	async function load() {
		loading = true;
		try {
			const [cmds, result] = await Promise.all([
				listPreCommands(),
				getTcAssignments(setLocation)
			]);
			preCommands = cmds;
			const ids = (result.tcPreCommandIds || '').split(',').map(s => parseInt(s.trim()) || 0);
			assignedId = (tcPosition < ids.length && ids[tcPosition] > 0) ? ids[tcPosition] : null;
		} catch { /* ignore */ }
		loading = false;
	}

	load();

	async function handleChange(e: Event) {
		const value = (e.target as HTMLSelectElement).value;
		saving = true;
		try {
			if (value) {
				await assignTc(Number(value), setLocation, tcPosition);
				assignedId = Number(value);
				toast.success('TC Pre-Command 설정됨');
			} else {
				await unassignTc(setLocation, tcPosition);
				assignedId = null;
				toast.success('TC Pre-Command 해제됨');
			}
			onAssignmentChanged?.();
		} catch {
			toast.error('설정 실패');
		}
		saving = false;
	}

	async function handleClear() {
		saving = true;
		try {
			await unassignTc(setLocation, tcPosition);
			assignedId = null;
			toast.success('TC Pre-Command 해제됨');
			onAssignmentChanged?.();
		} catch {
			toast.error('해제 실패');
		}
		saving = false;
	}
</script>

{#if loading}
	<span class="text-[9px] text-muted-foreground">...</span>
{:else if !canEdit}
	{#if assignedId}
		{@const cmd = preCommands.find(c => c.id === assignedId)}
		<span class="text-[9px] text-muted-foreground">{cmd?.name ?? '-'}</span>
	{:else}
		<span class="text-[9px] text-muted-foreground/40">-</span>
	{/if}
{:else}
	<div class="flex items-center gap-0.5">
		<select
			class="h-5 rounded border border-input bg-background px-1 text-[9px] max-w-[80px]"
			value={assignedId ?? ''}
			onchange={handleChange}
			disabled={saving}
		>
			<option value="">-</option>
			{#each preCommands as cmd}
				<option value={cmd.id}>{cmd.name}</option>
			{/each}
		</select>
		{#if assignedId}
			<button onclick={handleClear} disabled={saving} class="p-0.5 rounded hover:bg-muted" title="해제">
				<XIcon class="size-2.5 text-muted-foreground" />
			</button>
		{/if}
	</div>
{/if}
