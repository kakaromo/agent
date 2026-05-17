export interface GroupByOption<TData = unknown> {
	key: string;
	label: string;
	getValue?: (row: TData) => string;
}

export interface ServerSideConfig {
	totalItems: number;
	pageSize: number;
	currentPage: number;
	onPageChange: (page: number, size: number) => void;
}

export interface ServerGroupByConfig {
	groups: Array<{ groupKey: string; groupValue: string; count: number }>;
	onGroupSelect: (groupValue: string, page: number, size: number) => void;
	selectedGroup?: string | null;
	groupPageData?: { content: unknown[]; page: { totalElements: number; totalPages: number; number: number; size: number } };
}
