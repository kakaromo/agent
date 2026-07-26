<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { getBasicOptions, getAdvancedOptions, getDefaultParams, type OptionDef } from './benchmarkOptions.js';
	import {
		fetchAppMacros, listBundledApks, listInstalledApps, fetchCurrentActivity,
		type AppMacro, type BundledApk, type InstalledApp
	} from '$lib/api/agent.js';
	import CircleHelpIcon from '@lucide/svelte/icons/circle-help';
	import IOTestEditor from './iotest/IOTestEditor.svelte';
	import AppSearchSelect from './AppSearchSelect.svelte';
	import type { IOTestConfig } from './iotest/types.js';

	let availableMacros = $state<AppMacro[]>([]);
	$effect(() => {
		fetchAppMacros().then(m => availableMacros = m).catch(() => {});
	});

	// 번들된 APK / 디바이스 설치 앱 — install_apk / uninstall_apk step 의 select 옵션.
	// dialog 가 열릴 때마다 lazy fetch.
	let bundledApks = $state<BundledApk[]>([]);
	let installedApps = $state<InstalledApp[]>([]);

	export interface StepForm {
		type: string;
		tool: string;
		formParams: Record<string, string>;
		extraText: string;
		showAdvanced: boolean;
		useFileFromStep: number | null;
		cleanupMode: 'all' | 'steps' | 'path';
		cleanupSteps: Set<number>;
		cleanupPath: string;
		traceEnabled: boolean;
		traceType: string;
		macroId?: number | null;
		macroName?: string;
		macroClearMode?: 'none' | 'force_stop' | 'clear';
		// 인라인 macro config (macroId 없이 events 를 직접 담은 경우 — import 된 시나리오 등).
		// proto AppMacroConfig shape 을 그대로 보존해 실행 시 되돌려 보낸다.
		macroInline?: Record<string, unknown> | null;
		iotestConfig?: IOTestConfig;
		// 요소 기반 탭(tap_element) 셀렉터 + 폴백 좌표. text 입력은 inputText.
		elementResourceId?: string;
		elementText?: string;
		elementContentDesc?: string;
		elementX?: number | null;
		elementY?: number | null;
		inputText?: string;
		inputSubmit?: boolean;   // 입력 후 Enter (검색 실행)
		// 패턴 매칭 (동적 콘텐츠 재현)
		elementMatchMode?: string;    // exact | contains | prefix | suffix | regex
		elementIndex?: number;
		elementContainerId?: string;
		// scroll 스텝 (유저처럼 피드 반복 스크롤)
		scrollDirection?: string;     // down | up
		scrollCount?: number;
		scrollPause?: number;         // 각 스크롤 사이 대기(초)
		scrollDuration?: number;      // 스와이프 속도(ms) — 작을수록 빠름
		// tap 스텝 (절대 좌표 탭 — 커스텀뷰/게임 등 요소 미노출 화면)
		tapX?: number | null;
		tapY?: number | null;
		// key 스텝 (범용 키 이벤트 — 뒤로/홈/일시정지 등)
		keycode?: number;
		// stop_app 스텝 (앱 완전 종료 — PIP 재생까지 중단)
		stopPackage?: string;
		// launch_app 스텝 (앱 깨끗하게 시작)
		launchPackage?: string;
		launchClearMode?: string;     // clear | cache | force_stop | none
		launchWaitSeconds?: number;
		launchWaitActivity?: string;  // 지정 시 이 activity 포커스까지 대기
	}

	interface Props {
		open: boolean;
		step: StepForm | null;
		stepIndex: number;
		allSteps: StepForm[];
		onSave: (step: StepForm) => void;
		onCancel: () => void;
		// install_apk / uninstall_apk step 에서 디바이스의 설치된 앱 목록을 가져오기 위해
		// serverId / deviceId 가 필요. 없으면 select 대신 free-text 입력만 제공.
		serverId?: number | null;
		deviceId?: string | null;
	}

	let { open = $bindable(), step, stepIndex, allSteps, onSave, onCancel, serverId = null, deviceId = null }: Props = $props();

	// Local working copy
	let local = $state<StepForm | null>(null);

	$effect(() => {
		if (open && step) {
			local = {
				...step,
				formParams: { ...step.formParams },
				cleanupSteps: new Set(step.cleanupSteps)
			};
		}
	});

	// install_apk / uninstall_apk 가 보이는 동안만 lazy fetch (open & 해당 type 일 때).
	$effect(() => {
		if (!open || !local) return;
		if (local.type === 'install_apk' && bundledApks.length === 0) {
			listBundledApks().then(v => bundledApks = v).catch(() => {});
		}
		if ((local.type === 'uninstall_apk' || local.type === 'launch_app' || local.type === 'stop_app') && installedApps.length === 0 && serverId != null && deviceId) {
			listInstalledApps(serverId, deviceId).then(v => installedApps = v).catch(() => {});
		}
	});

	// launch_app 의 "대기할 Activity" 를 현재 디바이스 화면에서 자동으로 채운다.
	let activityLoading = $state(false);
	let activityHint = $state('');
	async function fillWaitActivity() {
		if (serverId == null || !deviceId || !local) {
			activityHint = '디바이스를 먼저 선택하세요';
			return;
		}
		activityLoading = true;
		activityHint = '';
		try {
			const act = await fetchCurrentActivity(serverId, deviceId);
			const value = act.component || act.package || act.raw;
			if (!value) { activityHint = '현재 activity 를 읽지 못했습니다'; return; }
			local.launchWaitActivity = value;
			activityHint = '채움: ' + value;
		} catch (e: any) {
			activityHint = '조회 실패: ' + (e?.message ?? '');
		} finally {
			activityLoading = false;
		}
	}

	function handleSave() {
		if (local) onSave(local);
	}

	function handleTypeChange(newType: string) {
		if (!local) return;
		local.type = newType;
		if (newType === 'benchmark') {
			local.formParams = getDefaultParams(local.tool);
		}
	}

	function handleToolChange(newTool: string) {
		if (!local) return;
		local = {
			...local,
			tool: newTool,
			formParams: newTool === 'IOTEST' ? {} : getDefaultParams(newTool),
			extraText: '',
			showAdvanced: false,
			useFileFromStep: null,
			traceEnabled: false,
			traceType: 'ufs',
			iotestConfig: newTool === 'IOTEST' ? { threads: [], duration_seconds: 0, sync_start: true } : undefined
		};
	}
</script>

{#snippet optionGrid(opts: OptionDef[])}
	{#if local}
		<div class="grid grid-cols-3 gap-x-3 gap-y-1.5">
			{#each opts as opt (opt.key)}
				<div class="flex items-center gap-1">
					<label class="text-[9px] w-16 shrink-0 text-right text-muted-foreground truncate" title={opt.label}>{opt.label}</label>
					{#if opt.type === 'select' && opt.choices}
						<select
							value={local.formParams[opt.key] ?? opt.defaultValue}
							onchange={(e) => { if (local) local.formParams = { ...local.formParams, [opt.key]: (e.target as HTMLSelectElement).value }; }}
							class="flex-1 border rounded px-1.5 py-0.5 text-[10px] bg-background min-w-0"
						>
							{#each opt.choices as c}<option value={c}>{c}</option>{/each}
						</select>
					{:else if opt.type === 'checkbox'}
						<input type="checkbox" checked={(local.formParams[opt.key] ?? opt.defaultValue) === '1'}
							onchange={(e) => { if (local) local.formParams = { ...local.formParams, [opt.key]: (e.target as HTMLInputElement).checked ? '1' : '0' }; }}
							class="size-3" />
					{:else}
						<input value={local.formParams[opt.key] ?? opt.defaultValue}
							oninput={(e) => { if (local) local.formParams = { ...local.formParams, [opt.key]: (e.target as HTMLInputElement).value }; }}
							class="flex-1 border rounded px-1.5 py-0.5 text-[10px] bg-background font-mono min-w-0" />
					{/if}
					<Tooltip.Provider>
						<Tooltip.Root>
							<Tooltip.Trigger><CircleHelpIcon class="size-2.5 text-muted-foreground cursor-help shrink-0" /></Tooltip.Trigger>
							<Tooltip.Content side="right" class="max-w-52 text-[10px]">{opt.help}</Tooltip.Content>
						</Tooltip.Root>
					</Tooltip.Provider>
				</div>
			{/each}
		</div>
	{/if}
{/snippet}

<Dialog.Root bind:open onOpenChange={(v) => { if (!v) onCancel(); }}>
	<Dialog.Content class="max-w-xl max-h-[80vh] flex flex-col">
		<Dialog.Header>
			<Dialog.Title class="text-sm">Step {stepIndex + 1} 편집</Dialog.Title>
		</Dialog.Header>

		{#if local}
			<div class="flex-1 overflow-y-auto space-y-3 py-2">
				<!-- Type + Tool -->
				<div class="flex gap-2">
					<div class="space-y-1">
						<label class="{sectionLabel}">Type</label>
						<select value={local.type} onchange={(e) => handleTypeChange((e.target as HTMLSelectElement).value)}
							class="border rounded px-2 py-1 text-xs bg-background w-32">
							<option value="benchmark">Benchmark</option>
							<option value="iotest">I/O Test</option>
							<option value="shell">Shell</option>
							<option value="cleanup">Cleanup</option>
							<option value="sleep">Sleep</option>
							<option value="trace_start">Trace Start</option>
							<option value="trace_stop">Trace Stop</option>
							<option value="launch_app">Launch App</option>
							<option value="stop_app">Stop App</option>
							<option value="app_macro">App Macro</option>
							<option value="tap_element">Tap Element</option>
							<option value="tap">Tap (좌표)</option>
							<option value="text">Text Input</option>
							<option value="scroll">Scroll</option>
							<option value="key">Key (뒤로/홈)</option>
							<option value="install_apk">Install APK</option>
							<option value="uninstall_apk">Uninstall APK</option>
						</select>
					</div>
					{#if local.type === 'benchmark'}
						<div class="space-y-1">
							<label class="{sectionLabel}">Tool</label>
							<select value={local.tool} onchange={(e) => handleToolChange((e.target as HTMLSelectElement).value)}
								class="border rounded px-2 py-1 text-xs bg-background w-24">
								<option value="FIO">fio</option>
								<option value="IOZONE">iozone</option>
								<option value="TIOTEST">tiotest</option>
							</select>
						</div>
					{/if}
				</div>

				<!-- Auto trace (benchmark, iotest, shell) -->
				{#if local.type === 'benchmark' || local.type === 'iotest' || local.type === 'shell'}
					<div class="flex items-center gap-2">
						<label class="flex items-center gap-1.5 cursor-pointer text-[10px]">
							<input type="checkbox" bind:checked={local.traceEnabled} class="size-3" />
							<span>Auto Trace</span>
						</label>
						{#if local.traceEnabled}
							<select bind:value={local.traceType} class="border rounded px-1.5 py-0.5 text-[10px] bg-background">
								<option value="ufs">UFS</option>
								<option value="block">Block</option>
								<option value="both">Both</option>
							</select>
						{/if}
					</div>
				{/if}

				<!-- I/O Test options -->
				{#if local.type === 'iotest'}
					{@const _init = local.iotestConfig ?? (local.iotestConfig = { threads: [], duration_seconds: 0, sync_start: true })}
					<IOTestEditor
						bind:config={local.iotestConfig}
						onUpdate={(c) => { if (local) local.iotestConfig = c; }} />

				<!-- Benchmark options -->
				{:else if local.type === 'benchmark'}
					<!-- File reuse -->
					{#if stepIndex > 0}
						<div class="flex items-center gap-2 text-[10px]">
							<label class="text-muted-foreground shrink-0">파일 재사용:</label>
							<select bind:value={local.useFileFromStep} class="border rounded px-1.5 py-0.5 text-[10px] bg-background">
								<option value={null}>없음 (새 파일 생성)</option>
								{#each allSteps.slice(0, stepIndex) as prev, pi}
									{#if prev.type === 'benchmark'}
										<option value={pi}>Step {pi + 1} ({prev.tool} {prev.formParams.rw ?? ''})</option>
									{/if}
								{/each}
							</select>
						</div>
					{/if}

					{@const basicOpts = getBasicOptions(local.tool)}
					{@const advOpts = getAdvancedOptions(local.tool)}

					<div class="space-y-1">
						<label class="{sectionLabel}">Basic Options</label>
						{@render optionGrid(basicOpts)}
					</div>

					{#if advOpts.length > 0}
						<div class="space-y-1">
							<button
								onclick={() => { if (local) local.showAdvanced = !local.showAdvanced; }}
								class="text-[10px] text-muted-foreground hover:text-foreground transition-colors"
							>
								Advanced Options {local.showAdvanced ? '▾' : '▸'} ({advOpts.length})
							</button>
							{#if local.showAdvanced}
								{@render optionGrid(advOpts)}
							{/if}
						</div>
					{/if}

					<div class="space-y-1">
						<label class="{sectionLabel}">Extra Parameters</label>
						<textarea bind:value={local.extraText}
							class="w-full border rounded px-2 py-1 text-[10px] bg-background font-mono h-12 resize-y"
							placeholder="key=value (줄바꿈 구분)"></textarea>
						<p class="text-[9px] text-muted-foreground">
							Loop 변수: 콤마 리스트 <code class="bg-muted px-0.5 rounded">4k,8k,16k</code> → loop마다 순서대로 적용.
							템플릿 <code class="bg-muted px-0.5 rounded">{'{'}</code>loop<code class="bg-muted px-0.5 rounded">{'}'}</code>G → loop 번호로 치환,
							<code class="bg-muted px-0.5 rounded">{'{'}</code>loop*1024<code class="bg-muted px-0.5 rounded">{'}'}</code> → 곱셈도 가능
						</p>
					</div>

				{:else if local.type === 'cleanup'}
					<div class="space-y-2">
						<label class="{sectionLabel}">Cleanup Mode</label>
						<div class="flex flex-col gap-1.5">
							<label class="flex items-center gap-1.5 text-[10px] cursor-pointer">
								<input type="radio" name="cleanup-mode" checked={local.cleanupMode === 'all'}
									onchange={() => { if (local) local.cleanupMode = 'all'; }} class="size-3" />
								전체 삭제 <span class="text-muted-foreground">(/data/local/tmp/test)</span>
							</label>
							<label class="flex items-center gap-1.5 text-[10px] cursor-pointer">
								<input type="radio" name="cleanup-mode" checked={local.cleanupMode === 'steps'}
									onchange={() => { if (local) local.cleanupMode = 'steps'; }} class="size-3" />
								특정 step 파일 삭제
							</label>
							{#if local.cleanupMode === 'steps'}
								<div class="pl-5 flex flex-wrap gap-1.5">
									{#each allSteps.slice(0, stepIndex) as prev, pi}
										{#if prev.type === 'benchmark'}
											<label class="flex items-center gap-0.5 text-[10px] cursor-pointer border rounded px-2 py-0.5
												{local.cleanupSteps.has(pi) ? 'bg-primary/10 border-primary' : 'hover:bg-muted'}">
												<input type="checkbox" checked={local.cleanupSteps.has(pi)}
													onchange={() => {
														if (!local) return;
														const next = new Set(local.cleanupSteps);
														if (next.has(pi)) next.delete(pi); else next.add(pi);
														local.cleanupSteps = next;
													}} class="size-2.5" />
												Step {pi + 1} <span class="text-muted-foreground">({prev.tool} {prev.formParams.rw ?? ''})</span>
											</label>
										{/if}
									{/each}
								</div>
							{/if}
							<label class="flex items-center gap-1.5 text-[10px] cursor-pointer">
								<input type="radio" name="cleanup-mode" checked={local.cleanupMode === 'path'}
									onchange={() => { if (local) local.cleanupMode = 'path'; }} class="size-3" />
								경로 직접 입력
							</label>
							{#if local.cleanupMode === 'path'}
								<input bind:value={local.cleanupPath}
									class="ml-5 border rounded px-2 py-1 text-[10px] bg-background font-mono"
									placeholder="/data/local/tmp/test/myfile" />
							{/if}
						</div>
					</div>

				{:else if local.type === 'trace_start'}
					<div class="flex items-center gap-2 text-[10px]">
						<label class="text-muted-foreground">Trace Type:</label>
						<select
							value={local.formParams.trace_type ?? 'ufs'}
							onchange={(e) => { if (local) local.formParams = { ...local.formParams, trace_type: (e.target as HTMLSelectElement).value }; }}
							class="border rounded px-1.5 py-0.5 text-[10px] bg-background"
						>
							<option value="ufs">UFS</option>
							<option value="block">Block</option>
							<option value="both">Both</option>
						</select>
					</div>

				{:else if local.type === 'trace_stop'}
					<div class="text-[10px] text-muted-foreground py-2">이전 trace_start를 자동으로 중지합니다</div>

				{:else if local.type === 'app_macro'}
					<div class="space-y-2">
						<label class="{sectionLabel}">Macro</label>
						{#if availableMacros.length > 0}
							<select
								value={local.macroId ?? ''}
								onchange={(e) => {
									const id = Number((e.target as HTMLSelectElement).value);
									const m = availableMacros.find(x => x.id === id);
									if (m && local) { local.macroId = m.id; local.macroName = m.name; }
								}}
								class="w-full border rounded px-2 py-1 text-xs bg-background"
							>
								<option value="">매크로 선택...</option>
								{#each availableMacros as m (m.id)}
									<option value={m.id}>{m.name}{m.packageName ? ` (${m.packageName})` : ''}</option>
								{/each}
							</select>
						{:else}
							<div class="{captionMuted}">저장된 매크로가 없습니다. Macro 탭에서 먼저 녹화해주세요.</div>
						{/if}
						{#if local.macroId}
							<div class="text-[9px] text-muted-foreground">선택됨: {local.macroName ?? `#${local.macroId}`}</div>
						{/if}

						<label class="text-[10px] font-medium text-muted-foreground uppercase tracking-wider mt-2">앱 초기화</label>
						<select
							value={local.macroClearMode ?? 'force_stop'}
							onchange={(e) => { if (local) local.macroClearMode = (e.target as HTMLSelectElement).value as any; }}
							class="w-full border rounded px-2 py-1 text-xs bg-background"
						>
							<option value="force_stop">앱 종료 후 실행 (데이터 유지)</option>
							<option value="clear">앱 데이터 초기화 후 실행 (pm clear)</option>
							<option value="none">초기화 없이 실행</option>
						</select>
					</div>

				{:else if local.type === 'install_apk'}
					<div class="space-y-2">
						<label class="{sectionLabel}">APK 파일</label>
						{#if bundledApks.length > 0}
							<select
								value={local.formParams.apk_filename ?? ''}
								onchange={(e) => { if (local) local.formParams = { ...local.formParams, apk_filename: (e.target as HTMLSelectElement).value }; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background"
							>
								<option value="">APK 선택...</option>
								{#each bundledApks as apk (apk.filename)}
									<option value={apk.filename}>{apk.filename}</option>
								{/each}
							</select>
							<p class="{captionMuted}">에이전트 호스트의 <code>tools/apks/</code> 폴더에서 발견된 .apk 파일</p>
						{:else}
							<input
								value={local.formParams.apk_filename ?? ''}
								oninput={(e) => { if (local) local.formParams = { ...local.formParams, apk_filename: (e.target as HTMLInputElement).value }; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background font-mono"
								placeholder="myapp.apk"
							/>
							<p class="{captionMuted}">번들된 APK 가 없습니다. 호스트의 <code>tools/apks/</code> 폴더에 .apk 파일을 추가하거나 파일명을 직접 입력하세요.</p>
						{/if}
						<label class="flex items-center gap-1.5 text-[10px] cursor-pointer mt-2">
							<input
								type="checkbox"
								checked={local.formParams.grant_permissions === 'true'}
								onchange={(e) => { if (local) local.formParams = { ...local.formParams, grant_permissions: (e.target as HTMLInputElement).checked ? 'true' : 'false' }; }}
								class="size-3"
							/>
							모든 런타임 권한 자동 부여 (<code>pm install -g</code>)
						</label>
					</div>

				{:else if local.type === 'uninstall_apk'}
					<div class="space-y-2">
						<label class="{sectionLabel}">Package Name</label>
						{#if installedApps.length > 0}
							<select
								value={local.formParams.package_name ?? ''}
								onchange={(e) => { if (local) local.formParams = { ...local.formParams, package_name: (e.target as HTMLSelectElement).value }; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background"
							>
								<option value="">앱 선택...</option>
								{#each installedApps as app (app.packageName)}
									<option value={app.packageName}>{app.appName || app.packageName} ({app.packageName})</option>
								{/each}
							</select>
						{:else}
							<input
								value={local.formParams.package_name ?? ''}
								oninput={(e) => { if (local) local.formParams = { ...local.formParams, package_name: (e.target as HTMLInputElement).value }; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background font-mono"
								placeholder="com.example.app"
							/>
							<p class="{captionMuted}">{deviceId ? '설치된 앱을 가져오지 못했습니다. 패키지명을 직접 입력하세요.' : '디바이스를 선택하면 설치된 앱 목록을 보여줍니다. 지금은 패키지명을 직접 입력하세요.'}</p>
						{/if}
						<label class="flex items-center gap-1.5 text-[10px] cursor-pointer mt-2">
							<input
								type="checkbox"
								checked={local.formParams.keep_data === 'true'}
								onchange={(e) => { if (local) local.formParams = { ...local.formParams, keep_data: (e.target as HTMLInputElement).checked ? 'true' : 'false' }; }}
								class="size-3"
							/>
							사용자 데이터/캐시 유지 (<code>pm uninstall -k</code>)
						</label>
					</div>

				{:else if local.type === 'tap'}
					<div class="space-y-2">
						<p class="{captionMuted}">
							지정한 화면 좌표를 직접 탭합니다. 삼성 노트 AI 메뉴처럼 요소가 안 잡히는 커스텀 화면·게임에 씁니다. 좌표는 디바이스 실제 픽셀 기준(스케일링 없음).
						</p>
						<div class="flex gap-2">
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">X</label>
								<input
									type="number" min="0"
									value={local.tapX ?? ''}
									oninput={(e) => { if (local) { const v=(e.target as HTMLInputElement).value; local.tapX = v===''?null:Number(v); } }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
									placeholder="예: 1157"
								/>
							</div>
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">Y</label>
								<input
									type="number" min="0"
									value={local.tapY ?? ''}
									oninput={(e) => { if (local) { const v=(e.target as HTMLInputElement).value; local.tapY = v===''?null:Number(v); } }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
									placeholder="예: 2115"
								/>
							</div>
						</div>
						<p class="{captionMuted}">
							💡 라이브 화면에서 요소를 클릭하면 tap_element 로 잡히지만, 요소가 없는 버튼은 이 tap 으로 좌표를 직접 넣으세요.
						</p>
					</div>

				{:else if local.type === 'tap_element'}
					<div class="space-y-2">
						<p class="{captionMuted}">
							재생 시 화면에서 요소를 다시 찾아 중심을 탭합니다. 우선순위: resource-id → text → content-desc.
							못 찾으면 아래 폴백 좌표로 탭합니다. (라이브 화면에서 요소를 클릭하면 자동으로 채워집니다.)
						</p>
						<div class="space-y-1">
							<label class="{sectionLabel}">Resource ID</label>
							<input
								value={local.elementResourceId ?? ''}
								oninput={(e) => { if (local) local.elementResourceId = (e.target as HTMLInputElement).value; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background font-mono"
								placeholder="com.example:id/button"
							/>
						</div>
						<div class="space-y-1">
							<label class="{sectionLabel}">Text</label>
							<input
								value={local.elementText ?? ''}
								oninput={(e) => { if (local) local.elementText = (e.target as HTMLInputElement).value; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background"
								placeholder="검색"
							/>
						</div>
						<div class="space-y-1">
							<label class="{sectionLabel}">Content Description</label>
							<input
								value={local.elementContentDesc ?? ''}
								oninput={(e) => { if (local) local.elementContentDesc = (e.target as HTMLInputElement).value; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background"
								placeholder="검색 버튼"
							/>
						</div>
						<div class="flex gap-2">
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">매칭 방식</label>
								<select
									value={local.elementMatchMode ?? 'exact'}
									onchange={(e) => { if (local) local.elementMatchMode = (e.target as HTMLSelectElement).value; }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								>
									<option value="exact">정확히 (기본)</option>
									<option value="contains">포함</option>
									<option value="prefix">~로 시작</option>
									<option value="suffix">~로 끝남 (동적 콘텐츠)</option>
									<option value="regex">정규식</option>
								</select>
							</div>
							<div class="space-y-1 w-24">
								<label class="{sectionLabel}">N번째</label>
								<input
									type="number"
									min="0"
									value={local.elementIndex ?? 0}
									oninput={(e) => { if (local) local.elementIndex = Number((e.target as HTMLInputElement).value) || 0; }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								/>
							</div>
						</div>
						{#if (local.elementMatchMode ?? 'exact') !== 'exact'}
							<p class="{captionMuted}">
								text/content-desc 를 패턴으로 해석합니다. 콘텐츠 값이 바뀌어도 같은 자리의 요소를 재현합니다.
							</p>
						{/if}
						<!-- 컨테이너는 라이브 화면에서 요소 클릭 시 자동 채워진다.
						     유저는 id 를 직접 알 필요가 없어 기본은 상태 표시만, 상세는 details 안에. -->
						{#if local.elementContainerId}
							<div class="flex items-center justify-between text-[10px] text-green-600 dark:text-green-400">
								<span>✓ 이 목록(스크롤 영역) 안에서만 검색</span>
								<button type="button" class="underline text-muted-foreground" onclick={() => { if (local) local.elementContainerId = ''; }}>지우기</button>
							</div>
						{/if}
						<details class="text-[10px]">
							<summary class="cursor-pointer text-muted-foreground">고급: 컨테이너 Resource ID 직접 지정</summary>
							<input
								value={local.elementContainerId ?? ''}
								oninput={(e) => { if (local) local.elementContainerId = (e.target as HTMLInputElement).value; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background font-mono mt-1"
								placeholder="라이브 화면에서 요소를 클릭하면 자동으로 채워집니다"
							/>
						</details>
						<div class="flex gap-2">
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">폴백 X</label>
								<input
									type="number"
									value={local.elementX ?? ''}
									oninput={(e) => { if (local) { const v = (e.target as HTMLInputElement).value; local.elementX = v === '' ? null : Number(v); } }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								/>
							</div>
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">폴백 Y</label>
								<input
									type="number"
									value={local.elementY ?? ''}
									oninput={(e) => { if (local) { const v = (e.target as HTMLInputElement).value; local.elementY = v === '' ? null : Number(v); } }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								/>
							</div>
						</div>
					</div>

				{:else if local.type === 'text'}
					<div class="space-y-1">
						<label class="{sectionLabel}">Input Text</label>
						<input
							value={local.inputText ?? ''}
							oninput={(e) => { if (local) local.inputText = (e.target as HTMLInputElement).value; }}
							class="w-full border rounded px-2 py-1 text-xs bg-background"
							placeholder="입력할 문자열 (input text)"
						/>
						<label class="flex items-center gap-1.5 text-[10px] cursor-pointer mt-1">
							<input
								type="checkbox"
								checked={local.inputSubmit ?? false}
								onchange={(e) => { if (local) local.inputSubmit = (e.target as HTMLInputElement).checked; }}
								class="size-3"
							/>
							입력 후 Enter 전송 (검색 실행)
						</label>
						<p class="{captionMuted}">공백은 자동으로 %s 로 변환됩니다. 특수문자는 이스케이프됩니다.</p>
					</div>

				{:else if local.type === 'scroll'}
					<div class="space-y-2">
						<p class="{captionMuted}">
							유저처럼 피드를 반복 스크롤합니다. tap_element 패턴과 함께 쓰면 "스크롤하다 영상 하나 열기" 같은 실사용 흐름을 재현합니다.
						</p>
						{#if (local.scrollCount ?? 3) >= 6}
							<p class="text-[10px] text-amber-600 dark:text-amber-400">
								⚠ 스크롤이 많으면(≥6회) 검색 결과처럼 짧은 목록에선 원하는 요소를 지나칠 수 있습니다. 검색→영상 흐름은 1~2회가 적당합니다.
							</p>
						{/if}
						<div class="flex gap-2">
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">방향</label>
								<select
									value={local.scrollDirection ?? 'down'}
									onchange={(e) => { if (local) local.scrollDirection = (e.target as HTMLSelectElement).value; }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								>
									<option value="down">아래로 (down)</option>
									<option value="up">위로 (up)</option>
								</select>
							</div>
							<div class="space-y-1 w-24">
								<label class="{sectionLabel}">횟수</label>
								<input
									type="number" min="1"
									value={local.scrollCount ?? 3}
									oninput={(e) => { if (local) local.scrollCount = Number((e.target as HTMLInputElement).value) || 1; }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								/>
							</div>
						</div>
						<div class="flex gap-2">
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">스크롤 간격 (초)</label>
								<input
									type="number" min="0" step="0.5"
									value={local.scrollPause ?? 1}
									oninput={(e) => { if (local) local.scrollPause = Number((e.target as HTMLInputElement).value) || 0; }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								/>
							</div>
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">속도 (ms, 작을수록 빠름)</label>
								<input
									type="number" min="50"
									value={local.scrollDuration ?? 400}
									oninput={(e) => { if (local) local.scrollDuration = Number((e.target as HTMLInputElement).value) || 400; }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								/>
							</div>
						</div>
					</div>

				{:else if local.type === 'key'}
					<div class="space-y-2">
						<p class="{captionMuted}">
							키 이벤트를 보냅니다. 뒤로가기로 재생 중단, 홈으로 세션 리셋 등 제어 동작에 씁니다.
						</p>
						<div class="space-y-1">
							<label class="{sectionLabel}">키</label>
							<select
								value={String(local.keycode ?? 4)}
								onchange={(e) => { if (local) local.keycode = Number((e.target as HTMLSelectElement).value); }}
								class="w-full border rounded px-2 py-1 text-xs bg-background"
							>
								<option value="86">▶ 재생 정지 (MEDIA_STOP) — 유튜브 PIP 재생도 멈춤 ✓</option>
								<option value="4">뒤로가기 (BACK) — 이전 화면 (⚠ 유튜브는 PIP로 계속 재생됨)</option>
								<option value="3">홈 (HOME) — 홈 화면으로 (⚠ PIP 재생 유지)</option>
								<option value="187">최근 앱 (RECENTS)</option>
								<option value="85">재생/일시정지 토글 (MEDIA_PLAY_PAUSE)</option>
								<option value="87">다음 (MEDIA_NEXT)</option>
								<option value="88">이전 (MEDIA_PREVIOUS)</option>
								<option value="24">볼륨 업</option>
								<option value="25">볼륨 다운</option>
								<option value="26">전원 (POWER)</option>
								<option value="66">엔터 (ENTER)</option>
							</select>
						</div>
						<div class="space-y-1">
							<label class="{sectionLabel}">직접 keycode 입력 (선택)</label>
							<input
								type="number" min="1"
								value={local.keycode ?? 4}
								oninput={(e) => { if (local) local.keycode = Number((e.target as HTMLInputElement).value) || 0; }}
								class="w-24 border rounded px-2 py-1 text-xs bg-background"
							/>
							<p class="{captionMuted}">
								⚠ 유튜브는 <b>뒤로가기/홈으로는 재생이 안 멈추고 PIP(작은 창)로 계속 재생</b>됩니다.
								재생 I/O 를 확실히 멈추려면 <b>재생 정지(MEDIA_STOP)</b> 또는 <b>앱 종료(stop_app)</b> 스텝을 쓰세요.
							</p>
						</div>
					</div>

				{:else if local.type === 'launch_app'}
					<div class="space-y-2">
						<p class="{captionMuted}">
							앱을 지정 초기화 모드로 깨끗하게 시작합니다. AnTuTu 등 벤치마크의 cold start 재현에 씁니다.
							시나리오 첫 스텝으로 두면 이후 tap_element/scroll 이 예측 가능한 상태에서 실행됩니다.
						</p>
						<div class="space-y-1">
							<label class="{sectionLabel}">패키지</label>
							<!-- 검색 가능한 콤보박스: 앱 이름/패키지명으로 검색, 목록에 없는 값도 직접 입력. -->
							<AppSearchSelect
								bind:value={local.launchPackage}
								apps={installedApps}
								placeholder="앱 이름 또는 패키지명 검색 (예: youtube)"
							/>
							<p class="{captionMuted}">
								{installedApps.length > 0 ? `실행 가능한 앱 ${installedApps.length}개 검색 가능 (시스템앱 포함). ` : ''}목록에 없는 패키지도 직접 입력할 수 있습니다.
							</p>
						</div>
						<div class="space-y-1">
							<label class="{sectionLabel}">초기화 모드</label>
							<select
								value={local.launchClearMode ?? 'force_stop'}
								onchange={(e) => { if (local) local.launchClearMode = (e.target as HTMLSelectElement).value; }}
								class="w-full border rounded px-2 py-1 text-xs bg-background"
							>
								<option value="clear">완전 초기화 — pm clear (데이터+캐시, cold start)</option>
								<option value="cache">캐시만 삭제 — pm clear --cache-only (데이터 유지)</option>
								<option value="force_stop">재시작 — force-stop (데이터 유지, warm start)</option>
								<option value="none">초기화 없이 실행</option>
							</select>
						</div>
						<div class="flex gap-2">
							<div class="space-y-1 w-28">
								<label class="{sectionLabel}">실행 후 대기 (초)</label>
								<input
									type="number" min="0"
									value={local.launchWaitSeconds ?? 3}
									oninput={(e) => { if (local) local.launchWaitSeconds = Number((e.target as HTMLInputElement).value) || 0; }}
									class="w-full border rounded px-2 py-1 text-xs bg-background"
								/>
							</div>
							<div class="space-y-1 flex-1">
								<label class="{sectionLabel}">대기할 Activity (선택)</label>
								<div class="flex gap-1">
									<input
										value={local.launchWaitActivity ?? ''}
										oninput={(e) => { if (local) local.launchWaitActivity = (e.target as HTMLInputElement).value; }}
										class="flex-1 border rounded px-2 py-1 text-xs bg-background font-mono"
										placeholder="HomeActivity (지정 시 이 화면 뜰 때까지 대기)"
									/>
									<button
										type="button"
										onclick={fillWaitActivity}
										disabled={activityLoading || !deviceId}
										class="shrink-0 border rounded px-2 py-1 text-xs hover:bg-muted disabled:opacity-50 whitespace-nowrap"
										title="선택된 디바이스의 현재 화면 activity 를 읽어 채웁니다">
										{activityLoading ? '조회 중…' : '현재 화면'}
									</button>
								</div>
								{#if activityHint}
									<p class="text-[10px] text-muted-foreground break-all">{activityHint}</p>
								{:else}
									<p class="text-[10px] text-muted-foreground">기다릴 화면을 기기에 띄운 뒤 "현재 화면"을 누르면 activity 를 자동으로 채웁니다.</p>
								{/if}
							</div>
						</div>
					</div>

				{:else if local.type === 'stop_app'}
					<div class="space-y-2">
						<p class="{captionMuted}">
							앱을 완전히 종료합니다 (force-stop). 유튜브처럼 뒤로가기로 안 멈추고 PIP(작은 창)로 계속 재생되는 경우도 확실히 중단됩니다. 워크로드 측정 종료 스텝으로 적합합니다.
						</p>
						<div class="space-y-1">
							<label class="{sectionLabel}">패키지</label>
							<AppSearchSelect
								bind:value={local.stopPackage}
								apps={installedApps}
								placeholder="앱 이름 또는 패키지명 검색 (예: youtube)"
							/>
							<p class="{captionMuted}">
								{installedApps.length > 0 ? `실행 가능한 앱 ${installedApps.length}개 검색 가능. ` : ''}비워두면 앞선 <b>Launch App</b> 의 패키지를 자동으로 종료합니다.
							</p>
						</div>
					</div>

				{:else}
					<!-- shell, sleep -->
					<div class="space-y-1">
						<label class="{sectionLabel}">
							{local.type === 'shell' ? 'Command' : 'Parameters'}
						</label>
						<textarea bind:value={local.extraText}
							class="w-full border rounded px-2 py-1 text-[10px] bg-background font-mono h-12 resize-y"
							placeholder={local.type === 'shell' ? 'cmd=...' : local.type === 'sleep' ? 'seconds=5' : ''}
						></textarea>
					</div>
				{/if}
			</div>

			<Dialog.Footer class="gap-2">
				<button onclick={onCancel} class="rounded-md border px-3 py-1.5 text-xs hover:bg-muted transition-colors">취소</button>
				<button onclick={handleSave} class="rounded-md bg-blue-600 text-white px-3 py-1.5 text-xs hover:bg-blue-700 transition-colors">저장</button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>
