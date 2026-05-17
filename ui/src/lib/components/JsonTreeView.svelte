<script lang="ts">
	import { untrack } from 'svelte';

	interface Props {
		jsonString: string;
	}

	let { jsonString }: Props = $props();

	const parseResult = $derived.by(() => {
		if (!jsonString.trim()) return { value: undefined, error: '' };
		try {
			return { value: JSON.parse(jsonString), error: '' };
		} catch (e) {
			return { value: undefined, error: (e as Error).message };
		}
	});

	const parsedValue = $derived(parseResult.value);
	const parseError = $derived(parseResult.error);

	let expandedPaths = $state<Set<string>>(new Set());
	let lastJsonString = '';

	// Auto-expand top 2 depths on new JSON
	$effect(() => {
		const val = parsedValue;
		const currentJson = jsonString;
		untrack(() => {
			if (currentJson === lastJsonString) return;
			lastJsonString = currentJson;
			if (val === undefined) {
				expandedPaths = new Set();
				return;
			}
			const paths = new Set<string>();
			function walk(v: unknown, path: string, depth: number) {
				if (depth > 2) return;
				if (v !== null && typeof v === 'object') {
					paths.add(path);
					if (Array.isArray(v)) {
						for (let i = 0; i < Math.min(v.length, 5); i++) {
							walk(v[i], `${path}[${i}]`, depth + 1);
						}
					} else {
						for (const key of Object.keys(v as Record<string, unknown>)) {
							walk((v as Record<string, unknown>)[key], `${path}.${key}`, depth + 1);
						}
					}
				}
			}
			walk(val, '$', 0);
			expandedPaths = paths;
		});
	});

	function togglePath(path: string) {
		const next = new Set(expandedPaths);
		if (next.has(path)) next.delete(path);
		else next.add(path);
		expandedPaths = next;
	}

	function typeColor(val: unknown): string {
		if (val === null || val === undefined) return 'text-zinc-400';
		if (typeof val === 'string') return 'text-green-600';
		if (typeof val === 'number') return 'text-blue-600';
		if (typeof val === 'boolean') return 'text-orange-600';
		return 'text-foreground';
	}

	function formatValue(val: unknown): string {
		if (val === null) return 'null';
		if (val === undefined) return 'undefined';
		if (typeof val === 'string') return `"${val.length > 60 ? val.slice(0, 60) + '...' : val}"`;
		if (typeof val === 'number' || typeof val === 'boolean') return String(val);
		return String(val);
	}

	function isExpandable(val: unknown): boolean {
		return val !== null && typeof val === 'object';
	}

	function summary(val: unknown): string {
		if (Array.isArray(val)) return `[${val.length} items]`;
		if (typeof val === 'object' && val !== null) return `{${Object.keys(val as Record<string, unknown>).length} keys}`;
		return '';
	}
</script>

<div class="h-48 font-mono text-xs rounded-md border bg-muted/30 overflow-auto">
	{#if !jsonString.trim()}
		<div class="flex items-center justify-center h-full text-muted-foreground text-xs">
			Text 탭에서 JSON을 붙여넣으세요
		</div>
	{:else if parseError}
		<div class="p-3 text-red-500 text-xs">
			<span class="font-semibold">Parse error:</span> {parseError}
		</div>
	{:else if parsedValue !== undefined}
		<div class="p-2">
			{#snippet treeNode(key: string, val: unknown, path: string)}
				<div class="flex items-start gap-0.5 leading-5">
					{#if isExpandable(val)}
						<button
							class="shrink-0 w-4 h-5 flex items-center justify-center text-muted-foreground hover:text-foreground"
							onclick={() => togglePath(path)}
						>
							{expandedPaths.has(path) ? '▼' : '▶'}
						</button>
					{:else}
						<span class="shrink-0 w-4"></span>
					{/if}

					<span class="text-violet-600">{key}</span>
					<span class="text-muted-foreground">:</span>

					{#if isExpandable(val) && !expandedPaths.has(path)}
						<button
							class="text-muted-foreground hover:text-foreground ml-1"
							onclick={() => togglePath(path)}
						>
							{summary(val)}
						</button>
					{:else if !isExpandable(val)}
						<span class="{typeColor(val)} ml-1">{formatValue(val)}</span>
					{/if}
				</div>

				{#if isExpandable(val) && expandedPaths.has(path)}
					<div class="ml-4 border-l border-border/50 pl-1">
						{#if Array.isArray(val)}
							{#each val as item, i}
								{#if i < 100}
									{@render treeNode(String(i), item, `${path}[${i}]`)}
								{:else if i === 100}
									<div class="leading-5 text-muted-foreground ml-4">
										... {val.length - 100} more items
									</div>
								{/if}
							{/each}
						{:else}
							{@const entries = Object.entries(val as Record<string, unknown>)}
							{#each entries as [k, v]}
								{@render treeNode(k, v, `${path}.${k}`)}
							{/each}
						{/if}
					</div>
				{/if}
			{/snippet}

			{#if Array.isArray(parsedValue)}
				{@render treeNode('root', parsedValue, '$')}
			{:else}
				{@const entries = Object.entries(parsedValue as Record<string, unknown>)}
				{#each entries as [k, v]}
					{@render treeNode(k, v, `$.${k}`)}
				{/each}
			{/if}
		</div>
	{/if}
</div>
