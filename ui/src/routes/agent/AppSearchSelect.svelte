<script lang="ts">
	import type { InstalledApp } from '$lib/api/agent.js';

	// 검색 가능한 앱 선택 콤보박스.
	// - 입력창에 앱 이름/패키지명 둘 다로 필터
	// - 아래 드롭다운에서 클릭 선택
	// - 목록에 없는 값(시스템앱 등)도 직접 입력 허용
	interface Props {
		value: string;                 // 선택된 패키지명 (bindable)
		apps: InstalledApp[];
		placeholder?: string;
	}
	let { value = $bindable(''), apps, placeholder = '앱 이름 또는 패키지명 검색' }: Props = $props();

	let open = $state(false);
	let query = $state('');
	let inputEl = $state<HTMLInputElement | null>(null);

	// 입력창에는 "앱이름 (패키지)" 형태로 보여주되, 실제 value 는 패키지명.
	// 사용자가 타이핑 중이면 query 를, 아니면 선택값 표시명을 보여준다.
	let selectedLabel = $derived.by(() => {
		const app = apps.find((a) => a.packageName === value);
		if (app) return `${app.appName || app.packageName} (${app.packageName})`;
		return value; // 목록에 없으면 패키지명 그대로
	});

	let display = $derived(open ? query : selectedLabel);

	let filtered = $derived.by(() => {
		const q = query.trim().toLowerCase();
		if (!q) return apps.slice(0, 50);
		return apps
			.filter(
				(a) =>
					(a.appName || '').toLowerCase().includes(q) ||
					a.packageName.toLowerCase().includes(q)
			)
			.slice(0, 50);
	});

	function pick(app: InstalledApp) {
		value = app.packageName;
		query = '';
		open = false;
	}

	function onInput(e: Event) {
		query = (e.target as HTMLInputElement).value;
		open = true;
		// 직접 입력한 값도 그대로 value 로 반영 (목록에 없는 패키지 허용)
		value = query;
	}

	function onFocus() {
		open = true;
		query = '';
	}
	function onBlur() {
		// 드롭다운 항목 클릭이 먼저 처리되도록 약간 지연
		setTimeout(() => (open = false), 150);
	}
</script>

<div class="relative">
	<input
		bind:this={inputEl}
		value={display}
		oninput={onInput}
		onfocus={onFocus}
		onblur={onBlur}
		{placeholder}
		class="w-full border rounded px-2 py-1 text-xs bg-background font-mono"
		autocomplete="off"
	/>
	{#if open && filtered.length > 0}
		<div
			class="absolute z-50 mt-0.5 w-full max-h-52 overflow-y-auto rounded border bg-background shadow-lg"
		>
			{#each filtered as app (app.packageName)}
				<button
					type="button"
					class="w-full text-left px-2 py-1 text-[11px] hover:bg-muted flex flex-col leading-tight {value ===
					app.packageName
						? 'bg-muted'
						: ''}"
					onmousedown={(e) => { e.preventDefault(); pick(app); }}
				>
					<span class="font-medium truncate">{app.appName || app.packageName}</span>
					<span class="text-muted-foreground font-mono text-[9px] truncate">{app.packageName}</span>
				</button>
			{/each}
		</div>
	{/if}
</div>
