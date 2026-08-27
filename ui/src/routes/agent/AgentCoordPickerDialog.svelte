<script lang="ts">
	import * as Dialog from '$lib/components/ui/dialog/index.js';
	import { untrack } from 'svelte';
	import { takeScreenshot, listUiElements, type UIElement } from '$lib/api/agent.js';
	import LoaderIcon from '@lucide/svelte/icons/loader-circle';
	import RefreshCwIcon from '@lucide/svelte/icons/refresh-cw';
	import CrosshairIcon from '@lucide/svelte/icons/crosshair';

	// 좌표 찍기 다이얼로그.
	//
	// tap 스텝의 X/Y 를 손으로 타이핑하지 않고 실제 화면을 보며 클릭해 넣는다.
	// 라이브 H.264 스트림(AgentScreenSheet)이 아니라 정지 스크린샷을 쓰는 이유:
	//  - 좌표는 정지 화면에서 조준하는 편이 정확하다 (스트림은 프레임이 흐른다)
	//  - 다이얼로그 안에서 WS/jmuxer 수명주기를 또 관리하지 않아도 된다
	// 요소 박스는 "조준 보조"로만 겹쳐 그린다 — 클릭 결과는 언제나 좌표다.

	interface Props {
		open: boolean;
		serverId: number | null;
		deviceId: string | null;
		// 현재 값(있으면 마커로 표시)
		initialX?: number | null;
		initialY?: number | null;
		onPick: (x: number, y: number) => void;
	}

	let { open = $bindable(), serverId, deviceId, initialX = null, initialY = null, onPick }: Props = $props();

	let loading = $state(false);
	let error = $state('');
	let imageSrc = $state('');
	let devW = $state(0);
	let devH = $state(0);

	// 선택된 좌표 (디바이스 픽셀)
	let pickedX = $state<number | null>(null);
	let pickedY = $state<number | null>(null);

	// 조준 보조용 요소 박스 (실패해도 좌표 찍기는 동작해야 하므로 optional)
	let elements = $state<UIElement[]>([]);
	let showBoxes = $state(true);
	let elemDevW = 0;
	let elemDevH = 0;

	let imgEl = $state<HTMLImageElement | null>(null);
	// 마우스 hover 좌표 (디바이스 픽셀) — 클릭 전에 어디를 찍는지 보여준다.
	let hoverX = $state<number | null>(null);
	let hoverY = $state<number | null>(null);

	// 열림 전환(closed → open)에서만 한 번 초기화 + 캡처한다.
	//
	// open 을 조건으로 쓰되 imageSrc/loading 같은 자기 출력값을 의존성으로 읽지 않는다.
	// 그렇게 하면 (a) 캡처 실패 시 error 만 남고 imageSrc 는 빈 채라 effect 가 무한 재시도하고,
	// (b) initialX 는 부모의 tapX 라서 좌표를 찍는 순간 값이 바뀌어 방금 찍은 좌표를
	//     예전 값으로 되돌려 버린다. wasOpen 으로 전환만 잡으면 둘 다 발생하지 않는다.
	let wasOpen = false;
	$effect(() => {
		if (open === wasOpen) return;
		wasOpen = open;
		if (open) {
			imageSrc = '';
			elements = [];
			error = '';
			hoverX = hoverY = null;
			// untrack: initialX/Y 는 부모의 tapX/tapY 라서 좌표를 찍으면 곧바로 바뀐다.
			// 의존성으로 잡히면 effect 가 재실행돼 방금 찍은 값을 되돌린다.
			pickedX = untrack(() => initialX) ?? null;
			pickedY = untrack(() => initialY) ?? null;
			capture();
		}
	});

	async function capture() {
		if (serverId == null || !deviceId) {
			error = '디바이스를 먼저 선택하세요';
			return;
		}
		loading = true;
		error = '';
		try {
			const shot = await takeScreenshot(serverId, deviceId);
			if (!shot?.success || !shot.imageBase64) {
				error = '스크린샷을 가져오지 못했습니다';
				return;
			}
			// width/height 는 agent 가 PNG 를 디코드해 얻은 실제 디바이스 픽셀이다.
			// 디코드 실패 시 0 이 오는데, 그대로 두면 좌표 환산이 조용히 틀린 값을 낸다.
			if (!shot.width || !shot.height) {
				error = '화면 크기를 읽지 못했습니다 (좌표를 계산할 수 없음)';
				imageSrc = '';
				return;
			}
			imageSrc = `data:image/png;base64,${shot.imageBase64}`;
			devW = shot.width;
			devH = shot.height;
			// 요소 박스는 부가 기능 — 실패해도 좌표 찍기는 계속된다.
			try {
				const res = await listUiElements(serverId, deviceId, true);
				if (res?.success) {
					elements = res.elements ?? [];
					elemDevW = res.deviceWidth || devW;
					elemDevH = res.deviceHeight || devH;
				}
			} catch { /* 조준 보조 실패는 무시 */ }
		} catch (e: any) {
			error = '캡처 실패: ' + (e?.message ?? '');
		} finally {
			loading = false;
		}
	}

	// 표시 이미지 좌표 → 디바이스 픽셀.
	// img 는 object-contain 이 아니라 실제 렌더 크기를 그대로 쓰므로
	// clientWidth/Height 대비 비율로 환산하면 된다.
	function toDevice(e: MouseEvent): { x: number; y: number } | null {
		if (!imgEl || devW <= 0 || devH <= 0) return null;
		const r = imgEl.getBoundingClientRect();
		if (r.width <= 0 || r.height <= 0) return null;
		const x = Math.round(((e.clientX - r.left) / r.width) * devW);
		const y = Math.round(((e.clientY - r.top) / r.height) * devH);
		// 화면 밖 클릭 방어 (테두리 걸침)
		if (x < 0 || y < 0 || x > devW || y > devH) return null;
		return { x, y };
	}

	function onImageClick(e: MouseEvent) {
		const p = toDevice(e);
		if (!p) return;
		pickedX = p.x;
		pickedY = p.y;
	}

	function onImageMove(e: MouseEvent) {
		const p = toDevice(e);
		hoverX = p?.x ?? null;
		hoverY = p?.y ?? null;
	}

	// 요소 박스 → 이미지 위 상대 % (이미지가 어떤 크기로 렌더되든 맞도록 % 로 그린다)
	function boxStyle(el: UIElement): string {
		const dw = elemDevW || devW;
		const dh = elemDevH || devH;
		if (dw <= 0 || dh <= 0) return 'display:none';
		const [x1, y1, x2, y2] = el.bounds;
		return `left:${(x1 / dw) * 100}%; top:${(y1 / dh) * 100}%; width:${((x2 - x1) / dw) * 100}%; height:${((y2 - y1) / dh) * 100}%;`;
	}

	function markerStyle(x: number, y: number): string {
		if (devW <= 0 || devH <= 0) return 'display:none';
		return `left:${(x / devW) * 100}%; top:${(y / devH) * 100}%;`;
	}

	function confirm() {
		if (pickedX == null || pickedY == null) return;
		onPick(pickedX, pickedY);
		open = false;
	}
</script>

<Dialog.Root bind:open>
	<Dialog.Content class="max-w-3xl max-h-[90vh] flex flex-col">
		<Dialog.Header class="pb-1">
			<Dialog.Title class="text-sm">화면에서 좌표 찍기</Dialog.Title>
			<Dialog.Description class="text-[10px]">
				디바이스 화면을 클릭하면 그 지점의 실제 픽셀 좌표가 tap 스텝에 들어갑니다. 요소가 잡히지 않는 커스텀 화면·게임용입니다.
			</Dialog.Description>
		</Dialog.Header>

		<div class="flex-1 min-h-0 flex gap-3 py-2">
			<!-- 화면 -->
			<div class="flex-1 min-h-0 flex items-center justify-center bg-muted/30 rounded overflow-hidden">
				{#if loading}
					<div class="flex flex-col items-center gap-2 text-xs text-muted-foreground">
						<LoaderIcon class="size-5 animate-spin" />
						화면 캡처 중...
					</div>
				{:else if error}
					<div class="text-center text-xs text-destructive space-y-2">
						<p>{error}</p>
						<button onclick={capture} class="rounded border px-3 py-1 text-xs hover:bg-muted">다시 시도</button>
					</div>
				{:else if imageSrc}
					<div class="relative max-h-full">
						<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
						<img
							bind:this={imgEl}
							src={imageSrc}
							alt="디바이스 화면"
							class="max-h-[60vh] w-auto block cursor-crosshair select-none"
							draggable="false"
							onclick={onImageClick}
							onmousemove={onImageMove}
							onmouseleave={() => { hoverX = hoverY = null; }}
						/>
						<!-- 조준 보조 요소 박스 (클릭은 이미지가 받도록 pointer-events 없음) -->
						{#if showBoxes}
							<div class="absolute inset-0 pointer-events-none">
								{#each elements as el, i (i)}
									<div class="absolute border border-fuchsia-500/35" style={boxStyle(el)}></div>
								{/each}
							</div>
						{/if}
						<!-- 선택 마커 -->
						{#if pickedX != null && pickedY != null && devW > 0 && devH > 0}
							<div class="absolute pointer-events-none -translate-x-1/2 -translate-y-1/2" style={markerStyle(pickedX, pickedY)}>
								<div class="size-4 rounded-full border-2 border-pink-500 bg-pink-500/30"></div>
							</div>
							<div class="absolute pointer-events-none bg-pink-500/70" style="left:{(pickedX / devW) * 100}%; top:0; width:1px; height:100%;"></div>
							<div class="absolute pointer-events-none bg-pink-500/70" style="top:{(pickedY / devH) * 100}%; left:0; height:1px; width:100%;"></div>
						{/if}
					</div>
				{:else}
					<p class="text-xs text-muted-foreground">화면 없음</p>
				{/if}
			</div>

			<!-- 사이드 정보 -->
			<div class="w-44 shrink-0 space-y-2 text-[10px]">
				<button
					onclick={capture}
					disabled={loading}
					class="w-full inline-flex items-center justify-center gap-1 rounded border px-2 py-1 hover:bg-muted disabled:opacity-50"
				>
					<RefreshCwIcon class="size-3 {loading ? 'animate-spin' : ''}" /> 화면 새로고침
				</button>

				<label class="flex items-center gap-1.5 cursor-pointer">
					<input type="checkbox" bind:checked={showBoxes} class="size-3" />
					요소 박스 표시 (조준 보조)
				</label>

				<div class="rounded border p-2 space-y-1">
					<div class="text-muted-foreground">디바이스 해상도</div>
					<div class="font-mono">{devW} × {devH}</div>
				</div>

				<div class="rounded border p-2 space-y-1">
					<div class="text-muted-foreground">커서</div>
					<div class="font-mono">
						{hoverX != null && hoverY != null ? `${hoverX}, ${hoverY}` : '—'}
					</div>
				</div>

				<div class="rounded border p-2 space-y-1 {pickedX != null ? 'border-pink-500/60' : ''}">
					<div class="text-muted-foreground flex items-center gap-1">
						<CrosshairIcon class="size-3" /> 선택한 좌표
					</div>
					<div class="font-mono text-xs">
						{pickedX != null && pickedY != null ? `${pickedX}, ${pickedY}` : '아직 없음'}
					</div>
				</div>

				<p class="text-muted-foreground leading-tight">
					좌표는 디바이스 실제 픽셀 기준입니다. 해상도가 다른 기기에서 재생하면 어긋날 수 있으니,
					요소가 잡히는 화면이면 tap_element 쪽이 안전합니다.
				</p>
			</div>
		</div>

		<Dialog.Footer class="pt-1">
			<button onclick={() => (open = false)} class="rounded border px-3 py-1 text-xs hover:bg-muted">취소</button>
			<button
				onclick={confirm}
				disabled={pickedX == null || pickedY == null}
				class="rounded bg-pink-600 text-white px-3 py-1 text-xs hover:bg-pink-700 disabled:opacity-50"
			>
				이 좌표 사용
			</button>
		</Dialog.Footer>
	</Dialog.Content>
</Dialog.Root>
