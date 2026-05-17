<script lang="ts">
	import Check from '@lucide/svelte/icons/check';
	import X from '@lucide/svelte/icons/x';
	import Loader2 from '@lucide/svelte/icons/loader-2';
	import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
	import OctagonX from '@lucide/svelte/icons/octagon-x';
	import CircleStop from '@lucide/svelte/icons/circle-stop';
	import Siren from '@lucide/svelte/icons/siren';
	import Pause from '@lucide/svelte/icons/pause';
	import BatteryCharging from '@lucide/svelte/icons/battery-charging';
	import Timer from '@lucide/svelte/icons/timer';
	import Unplug from '@lucide/svelte/icons/unplug';
	import { getStateBadgeClass } from '$lib/config/slotState.js';

	interface Props {
		result: string;
	}

	const { result }: Props = $props();

	const cls = $derived(getStateBadgeClass(result));
	const lower = $derived(result.toLowerCase());

	type IconType = 'check' | 'x' | 'loader' | 'warn' | 'octagonx' | 'stop' | 'siren' | 'pause' | 'charge' | 'timer' | 'unplug' | 'none';

	// contains 기반 매칭 — 우선순위 순서 (구체적인 것 먼저)
	const icon: IconType = $derived.by(() => {
		const s = lower;
		if (s.includes('critical')) return 'siren';
		if (s.includes('emergency')) return 'siren';
		if (s.includes('timeout') && s.includes('fail')) return 'timer';
		if (s.includes('booting') && s.includes('fail')) return 'octagonx';
		if (s.includes('warning') && s.includes('pass')) return 'warn';
		if (s.includes('pass')) return 'check';
		if (s.includes('fail')) return 'x';
		if (s.includes('running')) return 'loader';
		if (s.includes('stop')) return 'stop';
		if (s.includes('warning')) return 'warn';
		if (s.includes('pause')) return 'pause';
		if (s.includes('charge') || s.includes('charging')) return 'charge';
		if (s.includes('disconnect') || s.includes('inactive')) return 'unplug';
		return 'none';
	});
</script>

<span class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium {cls}">
	{#if icon === 'check'}
		<Check class="size-2.5" />
	{:else if icon === 'x'}
		<X class="size-2.5" />
	{:else if icon === 'loader'}
		<Loader2 class="size-2.5 animate-spin" />
	{:else if icon === 'warn'}
		<TriangleAlert class="size-2.5" />
	{:else if icon === 'timer'}
		<Timer class="size-2.5" />
	{:else if icon === 'octagonx'}
		<OctagonX class="size-2.5" />
	{:else if icon === 'siren'}
		<Siren class="size-2.5" />
	{:else if icon === 'stop'}
		<CircleStop class="size-2.5" />
	{:else if icon === 'pause'}
		<Pause class="size-2.5" />
	{:else if icon === 'charge'}
		<BatteryCharging class="size-2.5" />
	{:else if icon === 'unplug'}
		<Unplug class="size-2.5" />
	{/if}
	{result}
</span>
