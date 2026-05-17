<script lang="ts">
	import XIcon from '@lucide/svelte/icons/x';
	import ChevronDownIcon from '@lucide/svelte/icons/chevron-down';
	import SearchIcon from '@lucide/svelte/icons/search';
	import CheckIcon from '@lucide/svelte/icons/check';

	interface Option {
		value: string;
		label: string;
	}

	interface Props {
		options: Option[];
		selected: string[];
		onchange: (selected: string[]) => void;
		placeholder?: string;
		class?: string;
	}

	let { options, selected, onchange, placeholder = '선택...', class: className = '' }: Props = $props();

	let open = $state(false);
	let search = $state('');
	let containerEl: HTMLDivElement | undefined;

	const filteredOptions = $derived.by(() => {
		if (!search.trim()) return options;
		const q = search.toLowerCase();
		return options.filter(o => o.label.toLowerCase().includes(q) || o.value.toLowerCase().includes(q));
	});

	function toggle(value: string) {
		if (selected.includes(value)) {
			onchange(selected.filter(v => v !== value));
		} else {
			onchange([...selected, value]);
		}
	}

	function remove(value: string) {
		onchange(selected.filter(v => v !== value));
	}

	function clearAll() {
		onchange([]);
	}

	function handleClickOutside(e: MouseEvent) {
		if (containerEl && !containerEl.contains(e.target as Node)) {
			open = false;
		}
	}

	function getLabel(value: string): string {
		return options.find(o => o.value === value)?.label ?? value;
	}
</script>

<svelte:window onclick={handleClickOutside} />

<div bind:this={containerEl} class="relative {className}">
	<!-- Trigger -->
	<button
		type="button"
		class="w-full flex items-center gap-1 min-h-[24px] px-1.5 text-[10px] rounded border bg-background text-left hover:border-primary/40 transition-colors"
		onclick={() => { open = !open; if (open) search = ''; }}
	>
		<div class="flex-1 flex items-center gap-0.5 flex-wrap min-w-0 py-0.5">
			{#if selected.length === 0}
				<span class="text-muted-foreground">{placeholder}</span>
			{:else if selected.length <= 2}
				{#each selected as v}
					<span class="inline-flex items-center gap-0.5 px-1 py-0 rounded bg-primary/10 text-primary text-[9px] max-w-[80px] truncate">
						{getLabel(v)}
						<button type="button" class="hover:text-destructive" onclick={(e) => { e.stopPropagation(); remove(v); }}>
							<XIcon class="size-2" />
						</button>
					</span>
				{/each}
			{:else}
				<span class="text-[9px] text-primary font-medium">{selected.length}개 선택됨</span>
			{/if}
		</div>
		{#if selected.length > 0}
			<button type="button" class="p-0.5 hover:text-destructive shrink-0" onclick={(e) => { e.stopPropagation(); clearAll(); }}>
				<XIcon class="size-2.5" />
			</button>
		{/if}
		<ChevronDownIcon class="size-3 text-muted-foreground shrink-0 transition-transform {open ? 'rotate-180' : ''}" />
	</button>

	<!-- Dropdown -->
	{#if open}
		<div class="absolute top-full left-0 right-0 mt-0.5 z-50 rounded-md border bg-background shadow-lg max-h-52 flex flex-col">
			<!-- Search -->
			<div class="flex items-center gap-1 px-2 py-1.5 border-b shrink-0">
				<SearchIcon class="size-3 text-muted-foreground shrink-0" />
				<!-- svelte-ignore a11y_autofocus -->
				<input
					type="text"
					bind:value={search}
					placeholder="검색..."
					class="flex-1 text-[10px] bg-transparent outline-none"
					autofocus
				/>
			</div>
			<!-- Options -->
			<div class="flex-1 overflow-y-auto py-0.5">
				{#each filteredOptions as opt (opt.value)}
					{@const isSelected = selected.includes(opt.value)}
					<button
						type="button"
						class="w-full flex items-center gap-1.5 px-2 py-1 text-[10px] text-left hover:bg-muted/50 transition-colors"
						onclick={() => toggle(opt.value)}
					>
						<span class="size-3.5 rounded border flex items-center justify-center shrink-0 {isSelected ? 'bg-primary border-primary' : ''}">
							{#if isSelected}<CheckIcon class="size-2.5 text-primary-foreground" />{/if}
						</span>
						<span class="truncate {isSelected ? 'font-medium' : ''}">{opt.label}</span>
					</button>
				{:else}
					<div class="text-center text-[10px] text-muted-foreground py-3">결과 없음</div>
				{/each}
			</div>
		</div>
	{/if}
</div>
