import type { EChartsOption } from 'echarts';

export interface ExcelSheet {
	name: string;
	sections: ExcelSection[];
}

export interface ExcelSection {
	type: 'image' | 'table';
	imageDataURL?: string;
	imageWidth?: number;
	imageHeight?: number;
	title?: string;
	headers: string[];
	rows: (string | number)[][];
}

const TITLE_FILL = { type: 'pattern' as const, pattern: 'solid' as const, fgColor: { argb: 'FF4A4A4A' } };
const TITLE_FONT = { bold: true, color: { argb: 'FFFFFFFF' }, size: 11 };
const HEADER_FILL = { type: 'pattern' as const, pattern: 'solid' as const, fgColor: { argb: 'FFD6E4F0' } };
const HEADER_FONT = { bold: true, size: 11 };
const EVEN_ROW_FILL = { type: 'pattern' as const, pattern: 'solid' as const, fgColor: { argb: 'FFF2F2F2' } };
const BORDER_STYLE = {
	top: { style: 'thin' as const, color: { argb: 'FFD0D0D0' } },
	bottom: { style: 'thin' as const, color: { argb: 'FFD0D0D0' } },
	left: { style: 'thin' as const, color: { argb: 'FFD0D0D0' } },
	right: { style: 'thin' as const, color: { argb: 'FFD0D0D0' } }
};

export async function exportToExcel(options: {
	fileName: string;
	sheets: ExcelSheet[];
}): Promise<void> {
	const ExcelJS = await import('exceljs');
	const workbook = new ExcelJS.Workbook();

	for (const sheet of options.sheets) {
		const ws = workbook.addWorksheet(sheet.name.slice(0, 31));
		let currentRow = 1;

		for (const section of sheet.sections) {
			if (section.type === 'image' && section.imageDataURL) {
				const base64 = section.imageDataURL.split(',')[1];
				if (base64) {
					const imageId = workbook.addImage({
						base64,
						extension: 'png'
					});
					const colSpan = section.imageWidth ?? 10;
					const rowSpan = section.imageHeight ?? 20;
					ws.addImage(imageId, {
						tl: { col: 0, row: currentRow - 1 },
						br: { col: colSpan, row: currentRow - 1 + rowSpan }
					});
					currentRow += rowSpan + 1;
				}
			} else if (section.type === 'table') {
				// Title row
				if (section.title) {
					const titleRow = ws.getRow(currentRow);
					for (let c = 1; c <= section.headers.length; c++) {
						const cell = titleRow.getCell(c);
						if (c === 1) cell.value = section.title;
						cell.fill = TITLE_FILL;
						cell.font = TITLE_FONT;
						cell.border = BORDER_STYLE;
					}
					currentRow++;
				}

				// Header row
				const headerRow = ws.getRow(currentRow);
				section.headers.forEach((h, i) => {
					const cell = headerRow.getCell(i + 1);
					cell.value = h;
					cell.fill = HEADER_FILL;
					cell.font = HEADER_FONT;
					cell.border = BORDER_STYLE;
				});
				currentRow++;

				// Data rows
				for (let r = 0; r < section.rows.length; r++) {
					const dataRow = ws.getRow(currentRow);
					section.rows[r].forEach((val, i) => {
						const cell = dataRow.getCell(i + 1);
						cell.value = typeof val === 'number' ? val : val;
						cell.border = BORDER_STYLE;
						if (r % 2 === 0) {
							cell.fill = EVEN_ROW_FILL;
						}
					});
					currentRow++;
				}

				// Gap after table
				currentRow += 1;
			}
		}

		// Auto-fit column widths
		ws.columns.forEach((col) => {
			let maxLen = 10;
			col.eachCell?.({ includeEmpty: false }, (cell) => {
				const len = String(cell.value ?? '').length;
				if (len > maxLen) maxLen = len;
			});
			col.width = Math.min(maxLen + 2, 40);
		});
	}

	const buffer = await workbook.xlsx.writeBuffer();
	const blob = new Blob([buffer], {
		type: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'
	});
	const url = URL.createObjectURL(blob);
	const a = document.createElement('a');
	a.href = url;
	a.download = options.fileName;
	a.click();
	URL.revokeObjectURL(url);
}

/**
 * 간단한 테이블 데이터를 Excel로 내보내기.
 * DataTable 페이지에서 전체 데이터를 export할 때 사용.
 */
export async function exportTableToExcel<T extends Record<string, any>>(options: {
	fileName: string;
	sheetName?: string;
	columns: { key: string; header: string }[];
	data: T[];
}): Promise<void> {
	const headers = options.columns.map(c => c.header);
	const rows = options.data.map(row =>
		options.columns.map(c => {
			const val = row[c.key];
			return val != null ? val : '';
		})
	);
	await exportToExcel({
		fileName: options.fileName,
		sheets: [{
			name: (options.sheetName ?? 'Sheet1').slice(0, 31),
			sections: [{ type: 'table', headers, rows }]
		}]
	});
}

export async function renderChartToImage(
	option: EChartsOption,
	width = 800,
	height = 420
): Promise<string> {
	const echarts = await import('echarts');
	// Ensure shine themes are registered on the echarts instance
	await import('$lib/components/perf-chart/shine-theme.js');
	const theme = 'shine';

	const div = document.createElement('div');
	div.style.width = `${width}px`;
	div.style.height = `${height}px`;
	div.style.position = 'absolute';
	div.style.left = '-9999px';
	div.style.top = '-9999px';
	document.body.appendChild(div);

	try {
		const chart = echarts.init(div, theme);
		chart.setOption(option);
		const dataURL = chart.getDataURL({
			type: 'png',
			pixelRatio: 2,
			backgroundColor: '#ffffff'
		});
		chart.dispose();
		return dataURL;
	} finally {
		document.body.removeChild(div);
	}
}
