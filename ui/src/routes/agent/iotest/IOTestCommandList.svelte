<script lang="ts">
	import { inputSm, captionMuted } from '$lib/styles/common.js';
	import { OP_DEFS, OP_CATEGORIES, getOpsByCategory } from './opDefs.js';
	import type { IOTestCommand } from './types.js';
	import PlusIcon from '@lucide/svelte/icons/plus';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
	import ChevronUpIcon from '@lucide/svelte/icons/chevron-up';

	interface Props {
		commands: IOTestCommand[];
		onUpdate: (cmds: IOTestCommand[]) => void;
		depth?: number;
	}

	let { commands = $bindable(), onUpdate, depth = 0 }: Props = $props();

	let expandedSet = $state<Set<string>>(new Set());

	function addCommand(op: string, target: IOTestCommand[], parentPath: string) {
		const cmd: IOTestCommand = { op };
		const def = OP_DEFS[op];
		if (def) {
			for (const f of def.fields) {
				if (f.defaultValue) {
					if (f.key === 'count' || f.key === 'blocks' || f.key === 'ms' || f.key === 'loop_count') {
						(cmd as any)[f.key] = parseInt(f.defaultValue) || f.defaultValue;
					} else {
						(cmd as any)[f.key] = f.defaultValue;
					}
				}
			}
		}
		if (op === 'loop') { cmd.loop_count = 10; cmd.commands = []; }
		if (op === 'if') { cmd.condition = '{{i % 2 == 0}}'; cmd.then = []; cmd.else = []; }
		target.push(cmd);
		commands = [...commands]; // trigger reactivity
		onUpdate(commands);
	}

	function removeAt(arr: IOTestCommand[], idx: number) {
		arr.splice(idx, 1);
		commands = [...commands];
		onUpdate(commands);
	}

	function updateField(cmd: IOTestCommand, key: string, value: any) {
		(cmd as any)[key] = value;
		commands = [...commands];
		onUpdate(commands);
	}

	function toggleExpand(key: string) {
		const s = new Set(expandedSet);
		if (s.has(key)) s.delete(key); else s.add(key);
		expandedSet = s;
	}

	function moveUp(arr: IOTestCommand[], idx: number) {
		if (idx === 0) return;
		[arr[idx - 1], arr[idx]] = [arr[idx], arr[idx - 1]];
		commands = [...commands];
		onUpdate(commands);
	}

	function moveDown(arr: IOTestCommand[], idx: number) {
		if (idx >= arr.length - 1) return;
		[arr[idx], arr[idx + 1]] = [arr[idx + 1], arr[idx]];
		commands = [...commands];
		onUpdate(commands);
	}

	function getOpLabel(op: string): string { return OP_DEFS[op]?.label ?? op; }
	function getOpColor(op: string): string { return OP_DEFS[op]?.color ?? 'bg-gray-100'; }

	function summary(cmd: IOTestCommand): string {
		switch (cmd.op) {
			case 'open': return cmd.path ?? '';
			case 'read': return `off=${cmd.offset ?? 0} bs=${cmd.bs ?? '4k'} x${cmd.count ?? 1}`;
			case 'write': return `off=${cmd.offset ?? 0} bs=${cmd.bs ?? '4k'} x${cmd.count ?? 1} ${cmd.pattern ?? 'zero'}`;
			case 'create_files': return `${cmd.count ?? 10} files in ${cmd.dir ?? ''}`;
			case 'delete_pattern': return `${cmd.rule ?? 'odd'} in ${cmd.dir ?? ''}`;
			case 'sysfs_write': return `${cmd.path ?? ''} = ${cmd.value ?? ''}`;
			case 'sysfs_read': return cmd.path ?? '';
			case 'shell': return (cmd.cmd ?? '').substring(0, 40);
			case 'sleep': return `${cmd.ms ?? 1000}ms`;
			case 'loop': return `x${cmd.loop_count ?? cmd.count ?? 1}${cmd.items?.length ? ` [${cmd.items.join(',')}]` : ''}`;
			case 'if': return cmd.condition ?? '';
			default: return '';
		}
	}
</script>

{#snippet commandList(cmds: IOTestCommand[], parentPath: string, indent: number)}
	<div class="space-y-1" style:padding-left="{indent * 12}px">
		{#each cmds as cmd, idx}
			{@const key = `${parentPath}.${idx}`}
			<div class="border rounded p-1.5 bg-background">
				<div class="flex items-center gap-1">
					<span class="text-[10px] text-muted-foreground w-4 text-right">{idx + 1}.</span>
					<span class="text-[9px] px-1.5 py-0.5 rounded font-medium {getOpColor(cmd.op)}">{getOpLabel(cmd.op)}</span>
					<span class="{captionMuted} flex-1 truncate">{summary(cmd)}</span>

					{#if cmd.op === 'loop' || cmd.op === 'if'}
						<button class="p-0.5 hover:bg-muted rounded" onclick={() => toggleExpand(key)}>
							{#if expandedSet.has(key)}
								<ChevronDownIcon class="w-3 h-3" />
							{:else}
								<ChevronRightIcon class="w-3 h-3" />
							{/if}
						</button>
					{/if}

					<button class="p-0.5 hover:bg-muted rounded disabled:opacity-30" onclick={() => moveUp(cmds, idx)} disabled={idx === 0}><ChevronUpIcon class="w-3 h-3" /></button>
					<button class="p-0.5 hover:bg-muted rounded disabled:opacity-30" onclick={() => moveDown(cmds, idx)} disabled={idx === cmds.length - 1}><ChevronDownIcon class="w-3 h-3" /></button>
					<button class="p-0.5 hover:bg-destructive/10 rounded text-destructive" onclick={() => removeAt(cmds, idx)}>
						<TrashIcon class="w-3 h-3" />
					</button>
				</div>

				<!-- Fields -->
				{#if OP_DEFS[cmd.op]?.fields.length}
					<div class="grid grid-cols-2 gap-x-3 gap-y-1 mt-1">
						{#each OP_DEFS[cmd.op].fields as field}
							<div class="flex items-center gap-1">
								<span class="text-[9px] w-14 shrink-0 text-right text-muted-foreground">{field.label}</span>
								{#if field.type === 'select' && field.choices}
									<select class={inputSm} value={(cmd as any)[field.key] ?? field.defaultValue}
										onchange={(e) => updateField(cmd, field.key, (e.target as HTMLSelectElement).value)}>
										{#each field.choices as c}<option value={c}>{c}</option>{/each}
									</select>
								{:else if field.type === 'textarea'}
									<textarea class="{inputSm} h-12 resize-none" value={(cmd as any)[field.key] ?? ''}
										oninput={(e) => updateField(cmd, field.key, (e.target as HTMLTextAreaElement).value)}
										placeholder={field.placeholder}></textarea>
								{:else}
									<input class={inputSm} value={(cmd as any)[field.key] ?? field.defaultValue}
										oninput={(e) => updateField(cmd, field.key, (e.target as HTMLInputElement).value)}
										placeholder={field.placeholder ?? field.help ?? ''} />
								{/if}
							</div>
						{/each}
					</div>
				{/if}

				<!-- Loop children -->
				{#if cmd.op === 'loop' && expandedSet.has(key)}
					<div class="mt-1 border-l-2 border-indigo-200 pl-1">
						{@render commandList(cmd.commands ?? [], key, indent + 1)}
						<div class="flex flex-wrap gap-1 mt-1 pl-1">
							{#each OP_CATEGORIES as cat}
								<div class="relative group">
									<button class="text-[9px] px-1.5 py-0.5 rounded border hover:bg-muted text-muted-foreground">
										<PlusIcon class="w-2.5 h-2.5 inline -mt-px" /> {cat}
									</button>
									<div class="absolute left-0 top-full mt-0.5 bg-popover border rounded shadow-lg z-10 hidden group-hover:block min-w-24">
										{#each getOpsByCategory(cat) as op}
											<button class="w-full text-left text-[9px] px-2 py-1 hover:bg-muted"
												onclick={() => { if (!cmd.commands) cmd.commands = []; addCommand(op, cmd.commands, key); }}>
												{OP_DEFS[op].label}
											</button>
										{/each}
									</div>
								</div>
							{/each}
						</div>
					</div>
				{/if}

				<!-- If branches -->
				{#if cmd.op === 'if' && expandedSet.has(key)}
					<div class="mt-1 space-y-1">
						<div class="border-l-2 border-green-300 pl-1">
							<span class="text-[9px] font-medium text-green-600">THEN</span>
							{@render commandList(cmd.then ?? [], `${key}.then`, indent + 1)}
							<div class="flex flex-wrap gap-1 mt-1 pl-1">
								{#each OP_CATEGORIES as cat}
									<div class="relative group">
										<button class="text-[9px] px-1.5 py-0.5 rounded border hover:bg-muted text-muted-foreground">
											<PlusIcon class="w-2.5 h-2.5 inline -mt-px" /> {cat}
										</button>
										<div class="absolute left-0 top-full mt-0.5 bg-popover border rounded shadow-lg z-10 hidden group-hover:block min-w-24">
											{#each getOpsByCategory(cat) as op}
												<button class="w-full text-left text-[9px] px-2 py-1 hover:bg-muted"
													onclick={() => { if (!cmd.then) cmd.then = []; addCommand(op, cmd.then, `${key}.then`); }}>
													{OP_DEFS[op].label}
												</button>
											{/each}
										</div>
									</div>
								{/each}
							</div>
						</div>
						<div class="border-l-2 border-red-300 pl-1">
							<span class="text-[9px] font-medium text-red-600">ELSE</span>
							{@render commandList(cmd.else ?? [], `${key}.else`, indent + 1)}
							<div class="flex flex-wrap gap-1 mt-1 pl-1">
								{#each OP_CATEGORIES as cat}
									<div class="relative group">
										<button class="text-[9px] px-1.5 py-0.5 rounded border hover:bg-muted text-muted-foreground">
											<PlusIcon class="w-2.5 h-2.5 inline -mt-px" /> {cat}
										</button>
										<div class="absolute left-0 top-full mt-0.5 bg-popover border rounded shadow-lg z-10 hidden group-hover:block min-w-24">
											{#each getOpsByCategory(cat) as op}
												<button class="w-full text-left text-[9px] px-2 py-1 hover:bg-muted"
													onclick={() => { if (!cmd.else) cmd.else = []; addCommand(op, cmd.else, `${key}.else`); }}>
													{OP_DEFS[op].label}
												</button>
											{/each}
										</div>
									</div>
								{/each}
							</div>
						</div>
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/snippet}

{@render commandList(commands, 'root', depth)}

<!-- Top-level add command -->
<div class="flex flex-wrap gap-1 mt-1" style:padding-left="{depth * 12}px">
	{#each OP_CATEGORIES as cat}
		<div class="relative group">
			<button class="text-[9px] px-1.5 py-0.5 rounded border hover:bg-muted text-muted-foreground">
				<PlusIcon class="w-2.5 h-2.5 inline -mt-px" /> {cat}
			</button>
			<div class="absolute left-0 top-full mt-0.5 bg-popover border rounded shadow-lg z-10 hidden group-hover:block min-w-24">
				{#each getOpsByCategory(cat) as op}
					<button class="w-full text-left text-[9px] px-2 py-1 hover:bg-muted"
						onclick={() => addCommand(op, commands, 'root')}>
						{OP_DEFS[op].label}
					</button>
				{/each}
			</div>
		</div>
	{/each}
</div>
