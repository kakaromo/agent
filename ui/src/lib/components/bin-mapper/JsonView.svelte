<script lang="ts">
	import type { MappedResult } from '$lib/api/binMapper.js';

	let { result }: { result: MappedResult | any } = $props();

	let copied = $state(false);
	let expandedKeys = $state<Set<string>>(new Set(['root']));

	function copyJson() {
		navigator.clipboard.writeText(JSON.stringify(result, null, 2));
		copied = true;
		setTimeout(() => (copied = false), 2000);
	}

	function toggleKey(key: string) {
		if (expandedKeys.has(key)) {
			expandedKeys.delete(key);
		} else {
			expandedKeys.add(key);
		}
		expandedKeys = new Set(expandedKeys);
	}

	function isExpandable(val: unknown): boolean {
		return val !== null && typeof val === 'object';
	}

	function getEntries(val: unknown): [string, unknown][] {
		if (Array.isArray(val)) return val.map((v, i) => [String(i), v]);
		if (val && typeof val === 'object') return Object.entries(val);
		return [];
	}

	function formatPrimitive(val: unknown): string {
		if (val === null) return 'null';
		if (typeof val === 'string') return `"${val}"`;
		return String(val);
	}

	function getSummary(val: unknown): string {
		if (Array.isArray(val)) return `[${val.length}]`;
		if (val && typeof val === 'object') return `{${Object.keys(val).length}}`;
		return '';
	}
</script>

<div class="relative">
	<button
		class="btn btn-ghost btn-xs absolute top-1 right-1"
		onclick={copyJson}
	>
		{copied ? 'Copied!' : 'Copy JSON'}
	</button>

	<div class="overflow-auto border rounded-lg bg-base-100 p-3 font-mono text-xs max-h-[600px]">
		{@render jsonNode(result, 'root', 0)}
	</div>
</div>

{#snippet jsonNode(val: unknown, path: string, depth: number)}
	{#if isExpandable(val)}
		<div style="padding-left: {depth * 12}px">
			<button class="hover:bg-base-200 rounded px-1" onclick={() => toggleKey(path)}>
				<span class="text-muted-foreground">{expandedKeys.has(path) ? '▼' : '▶'}</span>
				<span class="text-muted-foreground">{getSummary(val)}</span>
			</button>
			{#if expandedKeys.has(path)}
				{#each getEntries(val) as [key, childVal]}
					<div style="padding-left: 12px" class="flex">
						<span class="text-primary shrink-0">"{key}"</span>
						<span class="text-muted-foreground mx-1">:</span>
						{#if isExpandable(childVal)}
							{@render jsonNode(childVal, `${path}.${key}`, 0)}
						{:else}
							<span class={typeof childVal === 'string' ? 'text-success' : typeof childVal === 'number' ? 'text-info' : 'text-warning'}>
								{formatPrimitive(childVal)}
							</span>
						{/if}
					</div>
				{/each}
			{/if}
		</div>
	{:else}
		<span class={typeof val === 'string' ? 'text-success' : typeof val === 'number' ? 'text-info' : 'text-warning'}>
			{formatPrimitive(val)}
		</span>
	{/if}
{/snippet}
