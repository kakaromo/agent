<script lang="ts">
	let {
		segments,
		size = 160,
		strokeWidth = 24,
		label = '',
		sublabel = ''
	}: {
		segments: { value: number; color: string; label: string }[];
		size?: number;
		strokeWidth?: number;
		label?: string;
		sublabel?: string;
	} = $props();

	let radius = $derived((size - strokeWidth) / 2);
	let circumference = $derived(2 * Math.PI * radius);
	let cx = $derived(size / 2);
	let cy = $derived(size / 2);
	let total = $derived(segments.reduce((sum, s) => sum + s.value, 0));

	let arcs = $derived.by(() => {
		let offset = 0;
		return segments.map((seg) => {
			const pct = total > 0 ? seg.value / total : 0;
			const dash = pct * circumference;
			const gap = circumference - dash;
			const rotation = offset * 360 - 90;
			offset += pct;
			return { ...seg, dash, gap, rotation, pct };
		});
	});
</script>

<div class="flex flex-col items-center gap-2">
	<svg width={size} height={size} viewBox="0 0 {size} {size}">
		<!-- Background ring -->
		<circle {cx} {cy} r={radius} fill="none" stroke="currentColor" stroke-width={strokeWidth} class="opacity-10" />
		<!-- Segments -->
		{#each arcs as arc}
			{#if arc.value > 0}
				<circle
					{cx} {cy} r={radius}
					fill="none"
					stroke={arc.color}
					stroke-width={strokeWidth}
					stroke-dasharray="{arc.dash} {arc.gap}"
					stroke-linecap="round"
					transform="rotate({arc.rotation} {cx} {cy})"
					class="transition-all duration-500"
				/>
			{/if}
		{/each}
		<!-- Center text -->
		{#if label}
			<text x={cx} y={cy - 6} text-anchor="middle" class="fill-foreground text-2xl font-bold" dominant-baseline="central">{label}</text>
			{#if sublabel}
				<text x={cx} y={cy + 16} text-anchor="middle" class="fill-muted-foreground text-xs" dominant-baseline="central">{sublabel}</text>
			{/if}
		{/if}
	</svg>
	<!-- Legend -->
	<div class="flex flex-wrap justify-center gap-3 text-xs">
		{#each arcs as arc}
			<div class="flex items-center gap-1.5">
				<span class="inline-block size-2.5 rounded-full" style="background-color: {arc.color}"></span>
				<span class="text-muted-foreground">{arc.label}</span>
				<span class="font-semibold">{arc.value}</span>
				{#if total > 0}
					<span class="text-muted-foreground">({Math.round(arc.pct * 100)}%)</span>
				{/if}
			</div>
		{/each}
	</div>
</div>
