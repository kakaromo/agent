<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { sectionLabel, captionMuted } from '$lib/styles/common.js';
	import * as Tooltip from '$lib/components/ui/tooltip/index.js';
	import { getBasicOptions, getAdvancedOptions, getDefaultParams, type OptionDef } from './benchmarkOptions.js';
	import {
		fetchAppMacros, listBundledApks, listInstalledApps,
		type AppMacro, type BundledApk, type InstalledApp
	} from '$lib/api/agent.js';
	import CircleHelpIcon from '@lucide/svelte/icons/circle-help';
	import IOTestEditor from './iotest/IOTestEditor.svelte';
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
		iotestConfig?: IOTestConfig;
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
		if (local.type === 'uninstall_apk' && installedApps.length === 0 && serverId != null && deviceId) {
			listInstalledApps(serverId, deviceId).then(v => installedApps = v).catch(() => {});
		}
	});

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
							<option value="app_macro">App Macro</option>
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
