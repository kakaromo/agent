<script lang="ts">
	interface Props {
		date: string | Date | null | undefined;
		format?: 'date' | 'datetime' | 'time';
	}

	const { date, format = 'datetime' }: Props = $props();

	function formatDate(d: string | Date | null | undefined): string {
		if (!d) return '-';

		const dateObj = typeof d === 'string' ? new Date(d) : d;

		if (isNaN(dateObj.getTime())) return '-';

		const dateStr = dateObj.toLocaleDateString('ko-KR', {
			year: 'numeric',
			month: '2-digit',
			day: '2-digit'
		});

		const timeStr = dateObj.toLocaleTimeString('ko-KR', {
			hour: '2-digit',
			minute: '2-digit'
		});

		if (format === 'datetime') return `${dateStr} ${timeStr}`;
		if (format === 'time') return timeStr;
		return dateStr;
	}

	const formatted = $derived(formatDate(date));
</script>

<span class="text-muted-foreground">{formatted}</span>
