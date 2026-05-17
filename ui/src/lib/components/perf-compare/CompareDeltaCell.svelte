<script lang="ts">
	interface Props {
		value: string;
		colorClass: string;
	}

	let { value, colorClass }: Props = $props();
</script>

<span class="tabular-nums font-medium {colorClass}">{value}</span>
