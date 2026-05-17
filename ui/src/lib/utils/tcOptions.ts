/**
 * TC option schema: type + static defaults for non-global options.
 * Types: 'number' (numeric input), 'string' (text input), 'select' (dropdown)
 */
export interface TcOptionSchemaDef {
	type: 'number' | 'string' | 'select';
	defaultValue: string;
	choices?: { text: string; value: string }[];
}

export const tcOptionSchema: Record<string, TcOptionSchemaDef> = {
	s: { type: 'number', defaultValue: '' },
	l: { type: 'number', defaultValue: '3' },
	t: { type: 'number', defaultValue: '0' },
};

/**
 * TC name-specific overrides.
 * pattern: substring match against TC name (case-insensitive, longest first)
 */
export interface TcNameOverride {
	pattern: string;
	defaults?: Record<string, string>;
	choices?: Record<string, { text: string; value: string }[]>;
}

export const tcNameOverrides: TcNameOverride[] = [
	// Example:
	// { pattern: 'SeqWrite', defaults: { l: '5' }, choices: { s: [...] } },
];

/** Find the best matching tcName override (longest pattern first) */
export function findTcNameOverride(tcName: string): TcNameOverride | undefined {
	if (!tcName) return undefined;
	const lower = tcName.toLowerCase();
	const sorted = [...tcNameOverrides].sort((a, b) => b.pattern.length - a.pattern.length);
	return sorted.find(e => lower.includes(e.pattern.toLowerCase()));
}

/** Get effective schema for a TC option key, with tcName-specific choices override */
export function getTcOptionSchemaDef(key: string, tcName?: string): TcOptionSchemaDef | undefined {
	const base = tcOptionSchema[key];
	if (!base || !tcName) return base;
	const override = findTcNameOverride(tcName);
	if (override?.choices?.[key]) {
		return { ...base, choices: override.choices[key] };
	}
	return base;
}

/** Validate a single TC option value against its schema type */
export function validateTcOptionValue(key: string, value: string, tcName?: string): string | null {
	if (value === '') return null;
	const schema = getTcOptionSchemaDef(key, tcName);
	if (!schema) return null;
	if (schema.type === 'number' && (isNaN(Number(value)) || value.trim() === '')) {
		return `${key}: must be a number`;
	}
	if (schema.type === 'select' && schema.choices) {
		const valid = schema.choices.some(c => c.value === value);
		if (!valid) return `${key}: invalid selection`;
	}
	return null;
}
