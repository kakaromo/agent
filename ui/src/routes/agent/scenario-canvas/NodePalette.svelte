<script lang="ts">
	import { STEP_TYPE_COLORS, STEP_CONTRACTS } from './types.js';
	import PlayIcon from '@lucide/svelte/icons/play';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import TrashIcon from '@lucide/svelte/icons/trash-2';
	import ClockIcon from '@lucide/svelte/icons/clock';
	import ScanSearchIcon from '@lucide/svelte/icons/scan-search';
	import SquareIcon from '@lucide/svelte/icons/square';
	import SmartphoneIcon from '@lucide/svelte/icons/smartphone';
	import RepeatIcon from '@lucide/svelte/icons/repeat';
	import GitBranchIcon from '@lucide/svelte/icons/git-branch';
	import FlaskConicalIcon from '@lucide/svelte/icons/flask-conical';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import PackageMinusIcon from '@lucide/svelte/icons/package-minus';
	import MousePointerClickIcon from '@lucide/svelte/icons/mouse-pointer-click';
	import PointerIcon from '@lucide/svelte/icons/pointer';
	import TypeIcon from '@lucide/svelte/icons/type';
	import MouseIcon from '@lucide/svelte/icons/mouse';
	import RocketIcon from '@lucide/svelte/icons/rocket';
	import CornerUpLeftIcon from '@lucide/svelte/icons/corner-up-left';
	import CircleStopIcon from '@lucide/svelte/icons/circle-stop';

	// 팔레트 항목은 Go 계약(scenario.Specs)에서 생성된 STEP_CONTRACTS 로 만든다.
	// 새 step 을 실행부에 추가하면 팔레트에도 자동으로 나타난다 — 예전엔 이 배열을
	// 손으로 고쳐야 해서 빠뜨리면 UI 에서 만들 수 없는 step 이 생겼다.
	const ICONS: Record<string, typeof PlayIcon> = {
		play: PlayIcon,
		'flask-conical': FlaskConicalIcon,
		terminal: TerminalIcon,
		'trash-2': TrashIcon,
		clock: ClockIcon,
		'scan-search': ScanSearchIcon,
		square: SquareIcon,
		rocket: RocketIcon,
		'circle-stop': CircleStopIcon,
		smartphone: SmartphoneIcon,
		'mouse-pointer-click': MousePointerClickIcon,
		pointer: PointerIcon,
		type: TypeIcon,
		mouse: MouseIcon,
		'corner-up-left': CornerUpLeftIcon,
		download: DownloadIcon,
		'package-minus': PackageMinusIcon
	};

	const stepTypes = STEP_CONTRACTS.map((c) => ({
		type: c.type,
		label: c.label,
		desc: c.desc,
		destructive: c.destructive,
		icon: ICONS[c.icon] ?? TerminalIcon
	}));

	function onDragStart(event: DragEvent, type: string) {
		if (!event.dataTransfer) return;
		event.dataTransfer.setData('application/step-type', type);
		event.dataTransfer.effectAllowed = 'move';
	}
</script>

<div class="w-28 shrink-0 border-r p-1.5 space-y-0.5 overflow-y-auto text-[10px] leading-tight">
	<div class="text-[8px] font-medium text-muted-foreground uppercase tracking-wider mb-1">Step Types</div>
	<!-- Loop group -->
	<div class="text-[8px] font-medium text-muted-foreground uppercase tracking-wider mb-0.5 mt-1.5">Control</div>
	<div
		draggable="true"
		ondragstart={(e) => onDragStart(e, '__loop__')}
		class="flex items-center gap-1 px-1.5 py-1 rounded border border-blue-300 cursor-grab hover:bg-blue-50 active:cursor-grabbing transition-colors"
	>
		<RepeatIcon class="size-2.5 text-blue-600 shrink-0" />
		<div class="min-w-0">
			<div class="text-[9px] font-medium truncate">Loop</div>
			<div class="text-[8px] text-muted-foreground truncate">반복 그룹</div>
		</div>
	</div>

	<div
		draggable="true"
		ondragstart={(e) => onDragStart(e, '__condition__')}
		class="flex items-center gap-1 px-1.5 py-1 rounded border border-amber-300 cursor-grab hover:bg-amber-50 active:cursor-grabbing transition-colors"
	>
		<GitBranchIcon class="size-2.5 text-amber-600 shrink-0" />
		<div class="min-w-0">
			<div class="text-[9px] font-medium truncate">Condition</div>
			<div class="text-[8px] text-muted-foreground truncate">조건 분기</div>
		</div>
	</div>

	<div class="border-t my-1.5"></div>

	{#each stepTypes as st}
		{@const colors = STEP_TYPE_COLORS[st.type] ?? STEP_TYPE_COLORS.shell}
		<div
			draggable="true"
			ondragstart={(e) => onDragStart(e, st.type)}
			class="flex items-center gap-1 px-1.5 py-1 rounded border cursor-grab hover:bg-muted/50 active:cursor-grabbing transition-colors"
		>
			<st.icon class="size-2.5 shrink-0 {colors.text}" />
			<div class="min-w-0">
				<div class="text-[9px] font-medium truncate">
					{st.label}{#if st.destructive}<span
							class="ml-0.5 text-red-600"
							title="파괴적 동작 — 앱/파일이 삭제됩니다">⚠</span
						>{/if}
				</div>
				<div class="text-[8px] text-muted-foreground truncate">{st.desc}</div>
			</div>
		</div>
	{/each}
</div>
