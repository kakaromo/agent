import type { EChartsOption } from 'echarts';

/**
 * Base chart option 생성. 공통 title/tooltip/legend/grid/dataZoom 설정.
 * 반환된 객체에 xAxis, yAxis, series 등을 spread로 추가하면 됨.
 */
export function baseChartOption(
	tcName: string,
	fw?: string,
	gridOverride?: Partial<{ top: number; bottom: number; left: number; right: number }>
): Partial<EChartsOption> {
	return {
		title: {
			text: tcName,
			subtext: fw ?? '',
			left: 'center',
			textStyle: { fontSize: 14 },
			subtextStyle: { fontSize: 11 }
		},
		tooltip: { trigger: 'axis' },
		legend: { bottom: 0, left: 'center', type: 'scroll' },
		grid: {
			top: fw ? 60 : 45,
			bottom: 80,
			left: 70,
			right: 20,
			...gridOverride
		},
		dataZoom: [{ type: 'inside' }]
	};
}

/**
 * Chart image를 캡처하고 Excel로 내보내기.
 * chartRef가 있으면 동기 캡처, 없으면 renderChartToImage fallback.
 */
export async function captureChartImage(
	chartRef: { getImageDataURL: () => string | null } | undefined,
	chartOption: EChartsOption
): Promise<string | null> {
	const { renderChartToImage } = await import('$lib/utils/excel-export');
	return chartRef?.getImageDataURL() ?? (await renderChartToImage(chartOption));
}
