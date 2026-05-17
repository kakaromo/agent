export type FieldRole = 'tab' | 'cycle' | 'data' | 'stat' | 'ignore';
export type FieldType = 'number' | 'number[]' | 'string' | 'null' | 'object';
export type TopLevelShape = 'object-of-arrays' | 'array-of-objects' | 'other';

export interface FieldNode {
	path: string[];
	key: string;
	type: FieldType;
	sample: unknown;
	role: FieldRole;
}

export interface TabInfo {
	key: string;
	label: string;
	yAxisUnit: string;
	fields: FieldNode[];
}

export interface AnalysisResult {
	shape: TopLevelShape;
	tabs: TabInfo[];
	cycleField: FieldNode | null;
	allFields: FieldNode[];
	error?: string;
}
