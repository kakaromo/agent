<script lang="ts">
	// AgentAppManager — APK install / uninstall 패널.
	// - 번들된 APK 목록(`tools/apks/*.apk`)을 보여주고 디바이스에 설치.
	// - 디바이스의 third-party 앱 목록을 보여주고 uninstall.
	// 시나리오 빌더와 매크로 빌더의 사이드 작업이라 가벼운 단일 패널로 구성.

	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import {
		listBundledApks, installApk, uninstallApk, listInstalledApps,
		type BundledApk, type InstalledApp
	} from '$lib/api/agent.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import PackageIcon from '@lucide/svelte/icons/package';
	import DownloadIcon from '@lucide/svelte/icons/download';
	import Trash2Icon from '@lucide/svelte/icons/trash-2';
	import RefreshIcon from '@lucide/svelte/icons/refresh-cw';
	import SmartphoneIcon from '@lucide/svelte/icons/smartphone';

	interface Props {
		serverId: number | null;
		selectedDevices: Set<string>;
	}
	let { serverId, selectedDevices }: Props = $props();

	const firstSelectedDevice = $derived([...selectedDevices][0] ?? '');

	let bundled = $state<BundledApk[]>([]);
	let installed = $state<InstalledApp[]>([]);
	let loadingBundled = $state(false);
	let loadingInstalled = $state(false);
	// 진행 중 작업: 키는 apk filename / package name
	let busy = $state<Record<string, 'install' | 'uninstall'>>({});

	async function refreshBundled() {
		loadingBundled = true;
		try {
			bundled = await listBundledApks();
		} catch (err) {
			toast.error(`APK 목록 조회 실패: ${err instanceof Error ? err.message : String(err)}`);
		} finally {
			loadingBundled = false;
		}
	}

	async function refreshInstalled() {
		if (!firstSelectedDevice || serverId == null) {
			installed = [];
			return;
		}
		loadingInstalled = true;
		try {
			installed = await listInstalledApps(serverId, firstSelectedDevice);
		} catch (err) {
			toast.error(`설치된 앱 조회 실패: ${err instanceof Error ? err.message : String(err)}`);
		} finally {
			loadingInstalled = false;
		}
	}

	async function handleInstall(apk: BundledApk) {
		if (!firstSelectedDevice) {
			toast.warning('디바이스를 먼저 선택해주세요');
			return;
		}
		busy = { ...busy, [apk.filename]: 'install' };
		try {
			const resp = await installApk({ deviceId: firstSelectedDevice, apkFilename: apk.filename });
			if (resp.success) {
				toast.success(`${apk.filename} 설치 완료${resp.packageName ? ` (${resp.packageName})` : ''}`);
				await refreshInstalled();
			} else {
				toast.error(`설치 실패: ${resp.message || 'unknown error'}`);
			}
		} catch (err) {
			toast.error(`설치 실패: ${err instanceof Error ? err.message : String(err)}`);
		} finally {
			const { [apk.filename]: _, ...rest } = busy;
			busy = rest;
		}
	}

	async function handleUninstall(app: InstalledApp) {
		if (!firstSelectedDevice) return;
		if (!confirm(`${app.appName || app.packageName} 을(를) 디바이스에서 삭제하시겠습니까?`)) return;
		busy = { ...busy, [app.packageName]: 'uninstall' };
		try {
			const resp = await uninstallApk({ deviceId: firstSelectedDevice, packageName: app.packageName });
			if (resp.success) {
				toast.success(`${app.packageName} 삭제 완료`);
				await refreshInstalled();
			} else {
				toast.error(`삭제 실패: ${resp.message || 'unknown error'}`);
			}
		} catch (err) {
			toast.error(`삭제 실패: ${err instanceof Error ? err.message : String(err)}`);
		} finally {
			const { [app.packageName]: _, ...rest } = busy;
			busy = rest;
		}
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
		if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
		return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;
	}

	onMount(() => {
		refreshBundled();
		refreshInstalled();
	});

	// 디바이스 선택이 바뀌면 installed list 다시 로드
	$effect(() => {
		void firstSelectedDevice;
		refreshInstalled();
	});
</script>

<div class="h-full flex flex-col overflow-hidden">
	<!-- Bundled APK section -->
	<div class="border-b">
		<div class="flex items-center justify-between px-4 py-3">
			<div>
				<h3 class="text-sm font-semibold">설치 가능한 APK</h3>
				<p class="text-[10px] text-muted-foreground mt-0.5">에이전트 호스트의 <code class="text-[10px] bg-muted px-1 rounded">tools/apks/</code> 폴더 — 파일을 추가하면 여기 표시됩니다</p>
			</div>
			<button onclick={refreshBundled}
				class="p-1.5 rounded hover:bg-muted transition-colors" title="새로고침">
				<RefreshIcon class="size-3.5 text-muted-foreground {loadingBundled ? 'animate-spin' : ''}" />
			</button>
		</div>

		{#if loadingBundled}
			<div class="px-4 py-8 flex items-center justify-center gap-2">
				<LoaderIcon class="size-4 animate-spin text-muted-foreground" />
				<span class="text-xs text-muted-foreground">APK 목록 로딩 중...</span>
			</div>
		{:else if bundled.length === 0}
			<div class="px-4 py-6 text-center">
				<p class="text-xs text-muted-foreground">번들된 APK 가 없습니다</p>
				<p class="text-[10px] text-muted-foreground/60 mt-1">에이전트 호스트의 <code>tools/apks/</code> 폴더에 .apk 파일을 추가하세요</p>
			</div>
		{:else}
			<div class="max-h-60 overflow-y-auto">
				{#each bundled as apk (apk.filename)}
					{@const isBusy = busy[apk.filename] === 'install'}
					<div class="flex items-center gap-3 px-4 py-2 hover:bg-muted/30 transition-colors">
						<div class="size-7 rounded bg-blue-500/10 flex items-center justify-center shrink-0">
							<PackageIcon class="size-3.5 text-blue-600" />
						</div>
						<div class="flex-1 min-w-0">
							<div class="text-xs font-mono truncate">{apk.filename}</div>
							<div class="text-[10px] text-muted-foreground">{formatSize(apk.sizeBytes)}</div>
						</div>
						<button
							onclick={() => handleInstall(apk)}
							disabled={isBusy || !firstSelectedDevice}
							class="inline-flex items-center gap-1 rounded bg-primary px-2 py-1 text-[11px] text-primary-foreground hover:bg-primary/90 disabled:opacity-40 transition-colors"
							title={firstSelectedDevice ? `${firstSelectedDevice} 에 설치` : '디바이스 선택 필요'}
						>
							{#if isBusy}
								<LoaderIcon class="size-3 animate-spin" />
								설치 중
							{:else}
								<DownloadIcon class="size-3" />
								설치
							{/if}
						</button>
					</div>
				{/each}
			</div>
		{/if}
	</div>

	<!-- Installed apps section -->
	<div class="flex-1 overflow-hidden flex flex-col">
		<div class="flex items-center justify-between px-4 py-3 border-b">
			<div>
				<h3 class="text-sm font-semibold">디바이스에 설치된 앱</h3>
				<p class="text-[10px] text-muted-foreground mt-0.5">{firstSelectedDevice || '디바이스를 선택해주세요'} · third-party 패키지만 표시</p>
			</div>
			<button onclick={refreshInstalled}
				disabled={!firstSelectedDevice}
				class="p-1.5 rounded hover:bg-muted transition-colors disabled:opacity-40" title="새로고침">
				<RefreshIcon class="size-3.5 text-muted-foreground {loadingInstalled ? 'animate-spin' : ''}" />
			</button>
		</div>

		{#if !firstSelectedDevice}
			<div class="flex-1 flex flex-col items-center justify-center text-center p-8 gap-2">
				<SmartphoneIcon class="size-8 text-muted-foreground/20" />
				<p class="text-xs text-muted-foreground">왼쪽에서 디바이스를 선택해주세요</p>
			</div>
		{:else if loadingInstalled}
			<div class="flex-1 flex items-center justify-center gap-2">
				<LoaderIcon class="size-4 animate-spin text-muted-foreground" />
				<span class="text-xs text-muted-foreground">설치된 앱 목록 로딩 중...</span>
			</div>
		{:else if installed.length === 0}
			<div class="flex-1 flex items-center justify-center text-center p-8">
				<p class="text-xs text-muted-foreground">설치된 third-party 앱이 없습니다</p>
			</div>
		{:else}
			<div class="flex-1 overflow-y-auto">
				{#each installed as app (app.packageName)}
					{@const isBusy = busy[app.packageName] === 'uninstall'}
					<div class="flex items-center gap-3 px-4 py-2 border-b hover:bg-muted/30 transition-colors">
						<div class="size-7 rounded bg-violet-500/10 flex items-center justify-center shrink-0">
							<PackageIcon class="size-3.5 text-violet-600" />
						</div>
						<div class="flex-1 min-w-0">
							<div class="text-xs font-medium truncate">{app.appName || app.packageName}</div>
							<div class="text-[10px] text-muted-foreground font-mono truncate">{app.packageName}</div>
						</div>
						<button
							onclick={() => handleUninstall(app)}
							disabled={isBusy}
							class="inline-flex items-center gap-1 rounded border border-destructive/30 text-destructive px-2 py-1 text-[11px] hover:bg-destructive/10 disabled:opacity-40 transition-colors"
							title="앱 제거"
						>
							{#if isBusy}
								<LoaderIcon class="size-3 animate-spin" />
								제거 중
							{:else}
								<Trash2Icon class="size-3" />
								제거
							{/if}
						</button>
					</div>
				{/each}
			</div>
		{/if}
	</div>
</div>
