<script lang="ts">
	let { size = 20, class: className = '', animate = false }: { size?: number; class?: string; animate?: boolean } = $props();
</script>

<svg
	xmlns="http://www.w3.org/2000/svg"
	viewBox="0 0 24 24"
	fill="none"
	width={size}
	height={size}
	class={className}
>
	<!-- M자 회로 폴리라인 -->
	{#if animate}
		<polyline
			points="6,18 6,7 12,13 18,7 18,18"
			stroke="#1428A0"
			stroke-width="2.1"
			stroke-linecap="round"
			stroke-linejoin="round"
			class="m-draw"
		/>
	{:else}
		<polyline
			points="6,18 6,7 12,13 18,7 18,18"
			stroke="#1428A0"
			stroke-width="2.1"
			stroke-linecap="round"
			stroke-linejoin="round"
		/>
	{/if}

	<!-- 궤도 링 (회전) -->
	<ellipse
		cx="12" cy="12.5" rx="10" ry="4"
		stroke="#2962FF"
		stroke-width="0.75"
		stroke-dasharray="2.5 2"
		opacity="0.7"
		class={animate ? 'orbit-rotate' : ''}
	/>

	<!-- 노드 -->
	{#if animate}
		<circle cx="6" cy="18" r="1.4" fill="#1428A0" class="node-pop" style="animation-delay: 0.8s" />
		<circle cx="18" cy="18" r="1.4" fill="#1428A0" class="node-pop" style="animation-delay: 1.0s" />
		<circle cx="12" cy="7" r="1.1" fill="#2962FF" class="node-pop" style="animation-delay: 0.5s" />
	{:else}
		<circle cx="6" cy="18" r="1.4" fill="#1428A0" />
		<circle cx="18" cy="18" r="1.4" fill="#1428A0" />
		<circle cx="12" cy="7" r="1.1" fill="#2962FF" />
	{/if}

	<!-- 연결선 + 외부 노드 -->
	<line x1="18" y1="12" x2="22.5" y2="12" stroke="#2962FF" stroke-width="0.7" opacity="0.45" />
	{#if animate}
		<circle cx="22.5" cy="12" r="1" fill="#2962FF" class="pulse-node" />
	{:else}
		<circle cx="22.5" cy="12" r="1" fill="#2962FF" opacity="0.55" />
	{/if}
</svg>

<style>
	.m-draw {
		stroke-dasharray: 50;
		stroke-dashoffset: 50;
		animation: draw 1.2s ease-out forwards;
	}

	@keyframes draw {
		to {
			stroke-dashoffset: 0;
		}
	}

	.orbit-rotate {
		transform-origin: 12px 12.5px;
		animation: orbit 8s linear infinite;
	}

	@keyframes orbit {
		to {
			transform: rotate(360deg);
		}
	}

	.node-pop {
		transform-origin: center;
		opacity: 0;
		animation: pop 0.3s ease-out forwards;
	}

	@keyframes pop {
		0% {
			opacity: 0;
			transform: scale(0);
		}
		70% {
			transform: scale(1.3);
		}
		100% {
			opacity: 1;
			transform: scale(1);
		}
	}

	.pulse-node {
		opacity: 0.55;
		animation: pulse 2s ease-in-out infinite;
	}

	@keyframes pulse {
		0%, 100% {
			opacity: 0.55;
			r: 1;
		}
		50% {
			opacity: 0.9;
			r: 1.5;
		}
	}
</style>
