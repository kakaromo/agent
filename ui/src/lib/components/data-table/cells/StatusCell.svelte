<script lang="ts">
	type Status = 'Processed' | 'Processing' | 'New' | 'Declined' | 'Closed' | string;

	interface Props {
		status: Status;
	}

	const { status }: Props = $props();

	const statusConfig: Record<string, { dot: string; text: string }> = {
		Processed: { dot: 'bg-emerald-500', text: 'text-emerald-700' },
		Processing: { dot: 'bg-blue-500', text: 'text-blue-700' },
		New: { dot: 'bg-amber-500', text: 'text-amber-700' },
		Declined: { dot: 'bg-red-500', text: 'text-red-700' },
		Closed: { dot: 'bg-gray-400', text: 'text-gray-600' }
	};

	const config = $derived(statusConfig[status] ?? { dot: 'bg-gray-400', text: 'text-gray-600' });
</script>

<div class="inline-flex items-center gap-1.5">
	<span class="size-2 rounded-full {config.dot}"></span>
	<span class="text-xs font-medium {config.text}">{status}</span>
</div>
