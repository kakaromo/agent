<script lang="ts">
	import type { Snippet } from 'svelte';
	import * as ContextMenu from '$lib/components/ui/context-menu/index.js';
	import Eye from '@lucide/svelte/icons/eye';
	import Play from '@lucide/svelte/icons/play';
	import Square from '@lucide/svelte/icons/square';
	import Settings from '@lucide/svelte/icons/settings';
	import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
	import RefreshCw from '@lucide/svelte/icons/refresh-cw';
	import Eraser from '@lucide/svelte/icons/eraser';
	import Upload from '@lucide/svelte/icons/upload';
	import Info from '@lucide/svelte/icons/info';
	import Bug from '@lucide/svelte/icons/bug';
	import StickyNote from '@lucide/svelte/icons/sticky-note';
	import TerminalIcon from '@lucide/svelte/icons/terminal';
	import ClipboardList from '@lucide/svelte/icons/clipboard-list';
	import ListChecks from '@lucide/svelte/icons/list-checks';
	import ZapIcon from '@lucide/svelte/icons/zap';
	import { DEBUG_REGISTRY } from '$lib/components/debugRegistry.js';
	import type { DebugType } from '$lib/api/admin.js';

	interface CtxMenuState {
		startTest: boolean;
		stopTest: boolean;
		setTR: boolean;
		setTC: boolean;
		initEnv: boolean;
		initSet: boolean;
		rebootSet: boolean;
		clear: boolean;
		getInfo: boolean;
		makeSet: boolean;
		memo: boolean;
		preCommand?: boolean;
		[key: string]: boolean | undefined;
	}

	interface Props {
		ctxMenu: CtxMenuState;
		debugTypes: DebugType[];
		trigger: Snippet;
		onShowDetail: () => void;
		onExec: (command: string, data: string) => void;
		onSetTR: () => void;
		onSetTC: () => void;
		onQuickSetTC: () => void;
		onPreCommand: () => void;
		onMakeSet: () => void;
		onDebug: (typeKey: string) => void;
		onMemo: () => void;
		onTerminal: () => void;
		onClear: () => void;
	}

	let {
		ctxMenu,
		debugTypes,
		trigger,
		onShowDetail,
		onExec,
		onSetTR,
		onSetTC,
		onQuickSetTC,
		onPreCommand,
		onMakeSet,
		onDebug,
		onMemo,
		onTerminal,
		onClear
	}: Props = $props();
</script>

<ContextMenu.Root>
	<ContextMenu.Trigger>
		{@render trigger()}
	</ContextMenu.Trigger>

	<ContextMenu.Portal>
		<ContextMenu.Content class="w-52">
			<ContextMenu.Item onclick={onShowDetail}>
				<Eye class="size-4 mr-2" />
				상세 보기
			</ContextMenu.Item>
			<ContextMenu.Separator />
			<ContextMenu.Item disabled={!ctxMenu.startTest} onclick={() => onExec('test', '')}>
				<Play class="size-4 mr-2" />
				Start Test
			</ContextMenu.Item>
			<ContextMenu.Item disabled={!ctxMenu.stopTest} onclick={() => onExec('stop', '')}>
				<Square class="size-4 mr-2" />
				Stop Test
			</ContextMenu.Item>

			<ContextMenu.Separator />

			<ContextMenu.Sub>
				<ContextMenu.SubTrigger><Settings class="size-4 mr-2" />Prepare Test</ContextMenu.SubTrigger>
				<ContextMenu.SubContent class="w-48">
					<ContextMenu.Item disabled={!ctxMenu.setTR} onclick={onSetTR}>
						<ClipboardList class="size-4 mr-2" />
						Set TR
					</ContextMenu.Item>
					<ContextMenu.Item disabled={!ctxMenu.setTC} onclick={onSetTC}>
						<ListChecks class="size-4 mr-2" />
						Set TC
					</ContextMenu.Item>
					<ContextMenu.Item disabled={!ctxMenu.setTC} onclick={onQuickSetTC}>
						<ListChecks class="size-4 mr-2" />
						Set TC (그룹)
					</ContextMenu.Item>
					<ContextMenu.Item disabled={!ctxMenu.initEnv} onclick={() => onExec('initenv', '0')}>
						<Settings class="size-4 mr-2" />
						Init Environment
					</ContextMenu.Item>
					<ContextMenu.Separator />
					<ContextMenu.Item onclick={onPreCommand}>
						<ZapIcon class="size-4 mr-2" />
						Pre-Command
					</ContextMenu.Item>
				</ContextMenu.SubContent>
			</ContextMenu.Sub>

			<ContextMenu.Sub>
				<ContextMenu.SubTrigger><RotateCcw class="size-4 mr-2" />Reset</ContextMenu.SubTrigger>
				<ContextMenu.SubContent class="w-36">
					<ContextMenu.Item disabled={!ctxMenu.initSet} onclick={() => onExec('initset', '0')}>
						<RotateCcw class="size-4 mr-2" />
						Init Set
					</ContextMenu.Item>
					<ContextMenu.Item disabled={!ctxMenu.rebootSet} onclick={() => onExec('rebootset', '0')}>
						<RefreshCw class="size-4 mr-2" />
						Reboot Set
					</ContextMenu.Item>
					<ContextMenu.Item disabled={!ctxMenu.clear} onclick={onClear}>
						<Eraser class="size-4 mr-2" />
						Clear
					</ContextMenu.Item>
				</ContextMenu.SubContent>
			</ContextMenu.Sub>

			<ContextMenu.Sub>
				<ContextMenu.SubTrigger><Upload class="size-4 mr-2" />Image</ContextMenu.SubTrigger>
				<ContextMenu.SubContent class="w-44">
					<ContextMenu.Item disabled={!ctxMenu.getInfo} onclick={() => onExec('getinfo', '0')}>
						<Info class="size-4 mr-2" />
						Get Info
					</ContextMenu.Item>
					<ContextMenu.Item disabled={!ctxMenu.makeSet} onclick={onMakeSet}>
						<Upload class="size-4 mr-2" />
						MakeSet
					</ContextMenu.Item>
				</ContextMenu.SubContent>
			</ContextMenu.Sub>

			{#if debugTypes.length > 0}
				<ContextMenu.Sub>
					<ContextMenu.SubTrigger><Bug class="size-4 mr-2" />Debug</ContextMenu.SubTrigger>
					<ContextMenu.SubContent class="w-36">
						{#each debugTypes as dt (dt.id)}
							<ContextMenu.Item
								disabled={!ctxMenu[dt.typeKey]}
								onclick={() => onDebug(dt.typeKey)}
							>
								{DEBUG_REGISTRY[dt.typeKey]?.label ?? dt.name}
							</ContextMenu.Item>
						{/each}
					</ContextMenu.SubContent>
				</ContextMenu.Sub>
			{/if}

			<ContextMenu.Separator />

			<ContextMenu.Item disabled={!ctxMenu.memo} onclick={onMemo}>
				<StickyNote class="size-4 mr-2" />
				Memo
			</ContextMenu.Item>
			<ContextMenu.Item onclick={onTerminal}>
				<TerminalIcon class="size-4 mr-2" />
				Terminal
			</ContextMenu.Item>
		</ContextMenu.Content>
	</ContextMenu.Portal>
</ContextMenu.Root>
