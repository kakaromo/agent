<script lang="ts">
	import { Skeleton } from '$lib/components/ui/skeleton/index.js';

	let {
		columns = 5,
		rows = 5
	}: {
		columns?: number;
		rows?: number;
	} = $props();
</script>

<div class="rounded-md border overflow-hidden">
	<table class="w-full text-xs">
		<thead>
			<tr class="border-b bg-muted/50">
				{#each Array(columns) as _, i}
					<th class="px-3 py-2 text-left">
						<Skeleton class="h-3 {i === 0 ? 'w-10' : 'w-16'}" />
					</th>
				{/each}
			</tr>
		</thead>
		<tbody class="divide-y divide-border">
			{#each Array(rows) as _}
				<tr>
					{#each Array(columns) as _, i}
						<td class="px-3 py-2">
							<Skeleton class="h-3 {i === 0 ? 'w-8' : i === columns - 1 ? 'w-12' : 'w-20'}" />
						</td>
					{/each}
				</tr>
			{/each}
		</tbody>
	</table>
</div>
