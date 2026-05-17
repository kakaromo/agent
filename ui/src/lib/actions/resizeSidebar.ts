/**
 * resizeSidebar — 사이드바 오른쪽 엣지에 드래그 핸들을 붙여 너비를 조정하는 action.
 *
 * <div use:resizeSidebar={{ get: () => width, set: w => width = w, min: 140, max: 520 }}>
 */
export interface ResizeSidebarParams {
	get: () => number;
	set: (width: number) => void;
	min?: number;
	max?: number;
	onStart?: () => void;
	onEnd?: (width: number) => void;
}

export function resizeSidebar(node: HTMLElement, params: ResizeSidebarParams) {
	let current = params;
	const handle = document.createElement('div');
	handle.className =
		'absolute top-0 right-0 h-full w-1.5 cursor-col-resize group/resize z-10';
	handle.innerHTML =
		'<div class="absolute inset-y-0 right-0 w-px bg-border group-hover/resize:bg-primary group-active/resize:bg-primary transition-colors"></div>';
	handle.setAttribute('role', 'separator');
	handle.setAttribute('aria-orientation', 'vertical');

	if (getComputedStyle(node).position === 'static') {
		node.style.position = 'relative';
	}
	node.appendChild(handle);

	let startX = 0;
	let startWidth = 0;
	let dragging = false;

	function onMove(e: PointerEvent) {
		if (!dragging) return;
		const dx = e.clientX - startX;
		const min = current.min ?? 120;
		const max = current.max ?? 640;
		const next = Math.min(Math.max(startWidth + dx, min), max);
		current.set(next);
	}

	function onUp() {
		if (!dragging) return;
		dragging = false;
		document.body.style.cursor = '';
		document.body.style.userSelect = '';
		window.removeEventListener('pointermove', onMove);
		window.removeEventListener('pointerup', onUp);
		current.onEnd?.(current.get());
	}

	function onDown(e: PointerEvent) {
		if (e.button !== 0) return;
		e.preventDefault();
		dragging = true;
		startX = e.clientX;
		startWidth = current.get();
		document.body.style.cursor = 'col-resize';
		document.body.style.userSelect = 'none';
		current.onStart?.();
		window.addEventListener('pointermove', onMove);
		window.addEventListener('pointerup', onUp);
	}

	handle.addEventListener('pointerdown', onDown);

	return {
		update(next: ResizeSidebarParams) {
			current = next;
		},
		destroy() {
			handle.removeEventListener('pointerdown', onDown);
			window.removeEventListener('pointermove', onMove);
			window.removeEventListener('pointerup', onUp);
			handle.remove();
		}
	};
}
