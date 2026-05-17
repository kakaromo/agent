import * as echarts from 'echarts';

export const sharedColors = [
	'#c12e34',
	'#e6b600',
	'#0098d9',
	'#2b821d',
	'#005eaa',
	'#339ca8',
	'#cda819',
	'#32a487'
];

const shineLight = {
	color: sharedColors,
	backgroundColor: 'transparent',
	title: {
		textStyle: { fontWeight: 'normal' as const, color: '#333' },
		subtextStyle: { color: '#999' }
	},
	legend: {
		textStyle: { color: '#666' }
	},
	categoryAxis: {
		axisLine: { lineStyle: { color: '#ccc' } },
		axisTick: { lineStyle: { color: '#ccc' } },
		axisLabel: { color: '#666' },
		nameTextStyle: { color: '#666' }
	},
	valueAxis: {
		axisLine: { lineStyle: { color: '#ccc' } },
		axisTick: { lineStyle: { color: '#ccc' } },
		axisLabel: { color: '#666' },
		splitLine: { lineStyle: { color: '#eee' } },
		nameTextStyle: { color: '#666' }
	},
	tooltip: {
		backgroundColor: 'rgba(0,0,0,0.75)',
		textStyle: { color: '#fff' },
		borderWidth: 0
	},
	dataZoom: {
		dataBackgroundColor: '#dedede',
		fillerColor: 'rgba(154,217,247,0.2)',
		handleColor: '#005eaa',
		textStyle: { color: '#666' }
	}
};

/** Register shine theme on a given echarts instance (idempotent) */
export function registerShineTheme(ec: typeof echarts) {
	ec.registerTheme('shine', shineLight);
}

// Auto-register on the main echarts instance
registerShineTheme(echarts);
