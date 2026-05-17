/**
 * Rubber-band (drag-to-select) action for grids of cards.
 *
 * Usage:
 *   <div use:rubberBandSelect={{
 *       cardSelector: '[data-slot-card]',
 *       idAttr: 'slotId',
 *       groupAttr: 'trKey',
 *       currentSelectedTrKey: () => selectedTrKey,
 *       onSelect: (ids) => selectedIds = ids,
 *       getCurrentSelection: () => selectedIds,
 *       onSuppressNextClick: () => { dragJustEnded = true; ... }
 *   }}>
 *
 * Behavior:
 *   - mousedown on empty grid area (not on a card) starts a pending drag
 *   - moving past DRAG_THRESHOLD pixels activates the rubber band
 *   - cards intersecting the rubber band in the same TR group get selected
 *   - ctrl/cmd + drag adds to current selection (within same group)
 *   - rubber band rectangle is rendered inside the action's host element (positioned fixed)
 */

const DRAG_THRESHOLD = 5;

export interface RubberBandOptions {
	/** CSS selector for cards that participate in selection (e.g. '[data-slot-card]') */
	cardSelector: string;
	/** Data attribute name on each card carrying its numeric id (e.g. 'slotId' → data-slot-id) */
	idAttr: string;
	/** Data attribute name on group wrapper that scopes the drag (e.g. 'trKey' → data-tr-key) */
	groupAttr: string;
	/** Current selected group key (used for additive drag within same group). Pass a getter. */
	currentSelectedTrKey: () => string | null;
	/** Called with new selected ids whenever the selection changes during drag. */
	onSelect: (ids: Set<number>) => void;
	/** Optional: returns current selection (used for additive drag). Pass a getter. */
	getCurrentSelection?: () => Set<number>;
	/** Optional: fired at mouseup if a drag actually happened, so caller can suppress the ensuing click. */
	onDragEnd?: () => void;
}

export function rubberBandSelect(node: HTMLElement, options: RubberBandOptions) {
	let opts = options;

	let isDragging = false;
	let pending = false;
	let additive = false;
	let startX = 0;
	let startY = 0;
	let currentX = 0;
	let currentY = 0;
	let dragGroupKey = '';

	const rect = document.createElement('div');
	rect.setAttribute('data-rubber-band', '');
	rect.style.position = 'fixed';
	rect.style.pointerEvents = 'none';
	rect.style.zIndex = '50';
	rect.style.border = '1px solid var(--primary)';
	rect.style.background = 'color-mix(in oklch, var(--primary) 10%, transparent)';
	rect.style.borderRadius = '2px';
	rect.style.display = 'none';

	function updateRectStyle() {
		rect.style.left = Math.min(startX, currentX) + 'px';
		rect.style.top = Math.min(startY, currentY) + 'px';
		rect.style.width = Math.abs(currentX - startX) + 'px';
		rect.style.height = Math.abs(currentY - startY) + 'px';
	}

	function onMouseDown(e: MouseEvent) {
		if (e.button !== 0) return;
		const target = e.target as HTMLElement;
		if (target.closest(opts.cardSelector)) return;

		const groupEl = target.closest<HTMLElement>(`[data-${kebab(opts.groupAttr)}]`);
		dragGroupKey = groupEl?.dataset[opts.groupAttr] ?? '';

		pending = true;
		additive = e.ctrlKey || e.metaKey;
		startX = e.clientX;
		startY = e.clientY;
		currentX = e.clientX;
		currentY = e.clientY;
		e.preventDefault();
	}

	function onMouseMove(e: MouseEvent) {
		if (!pending && !isDragging) return;
		currentX = e.clientX;
		currentY = e.clientY;

		if (pending) {
			const dx = e.clientX - startX;
			const dy = e.clientY - startY;
			if (Math.sqrt(dx * dx + dy * dy) >= DRAG_THRESHOLD) {
				isDragging = true;
				pending = false;
				rect.style.display = 'block';
				document.body.appendChild(rect);
			}
		}

		if (isDragging) {
			updateRectStyle();
			computeSelection();
		}
	}

	function onMouseUp() {
		const wasDragging = isDragging;
		if (wasDragging) {
			computeSelection();
			opts.onDragEnd?.();
		}
		isDragging = false;
		pending = false;
		if (rect.parentNode) rect.parentNode.removeChild(rect);
	}

	function computeSelection() {
		const selector = dragGroupKey
			? `[data-${kebab(opts.groupAttr)}="${cssEscape(dragGroupKey)}"]`
			: null;
		const scope = selector ? node.querySelector<HTMLElement>(selector) : node;
		if (!scope) return;

		const left = Math.min(startX, currentX);
		const top = Math.min(startY, currentY);
		const right = Math.max(startX, currentX);
		const bottom = Math.max(startY, currentY);

		const currentTrKey = opts.currentSelectedTrKey();
		const next =
			additive && currentTrKey === dragGroupKey && opts.getCurrentSelection
				? new Set(opts.getCurrentSelection())
				: new Set<number>();

		const cards = scope.querySelectorAll<HTMLElement>(opts.cardSelector);
		cards.forEach((card) => {
			const cr = card.getBoundingClientRect();
			const overlaps =
				cr.left < right && cr.right > left && cr.top < bottom && cr.bottom > top;
			if (overlaps) {
				const idStr = card.dataset[opts.idAttr];
				const id = Number(idStr);
				if (!isNaN(id)) next.add(id);
			}
		});

		opts.onSelect(next);
	}

	node.addEventListener('mousedown', onMouseDown);
	window.addEventListener('mousemove', onMouseMove);
	window.addEventListener('mouseup', onMouseUp);

	return {
		update(newOptions: RubberBandOptions) {
			opts = newOptions;
		},
		destroy() {
			node.removeEventListener('mousedown', onMouseDown);
			window.removeEventListener('mousemove', onMouseMove);
			window.removeEventListener('mouseup', onMouseUp);
			if (rect.parentNode) rect.parentNode.removeChild(rect);
		}
	};
}

function kebab(camel: string): string {
	return camel.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase());
}

function cssEscape(value: string): string {
	if (typeof CSS !== 'undefined' && CSS.escape) return CSS.escape(value);
	return value.replace(/["\\]/g, '\\$&');
}
