/**
 * Largest Triangle Three Buckets (LTTB) downsampling algorithm.
 * Preserves the visual shape of the data while reducing point count.
 *
 * Reference: Sveinn Steinarsson, "Downsampling Time Series for Visual Representation" (2013)
 */

export function lttb(data: [number, number][], threshold: number): [number, number][] {
	if (threshold >= data.length || threshold < 3) return data;

	const sampled: [number, number][] = [];
	const bucketSize = (data.length - 2) / (threshold - 2);

	// Always keep the first point
	sampled.push(data[0]);

	let prevIndex = 0;

	for (let i = 0; i < threshold - 2; i++) {
		// Calculate the average point of the next bucket (used as target)
		const avgStart = Math.floor((i + 1) * bucketSize) + 1;
		const avgEnd = Math.min(Math.floor((i + 2) * bucketSize) + 1, data.length);

		let avgX = 0;
		let avgY = 0;
		const avgCount = avgEnd - avgStart;

		for (let j = avgStart; j < avgEnd; j++) {
			avgX += data[j][0];
			avgY += data[j][1];
		}
		avgX /= avgCount;
		avgY /= avgCount;

		// Find the point in the current bucket with the largest triangle area
		const rangeStart = Math.floor(i * bucketSize) + 1;
		const rangeEnd = Math.min(Math.floor((i + 1) * bucketSize) + 1, data.length);

		const prevX = data[prevIndex][0];
		const prevY = data[prevIndex][1];

		let maxArea = -1;
		let bestIndex = rangeStart;

		for (let j = rangeStart; j < rangeEnd; j++) {
			const area = Math.abs(
				(prevX - avgX) * (data[j][1] - prevY) - (prevX - data[j][0]) * (avgY - prevY)
			);
			if (area > maxArea) {
				maxArea = area;
				bestIndex = j;
			}
		}

		sampled.push(data[bestIndex]);
		prevIndex = bestIndex;
	}

	// Always keep the last point
	sampled.push(data[data.length - 1]);

	return sampled;
}
