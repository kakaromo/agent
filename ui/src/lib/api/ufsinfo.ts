import { get, post, put, del } from './client.js';
import type { CellType, Controller, Density, NandSize, NandType, UfsVersion, Oem } from './types.js';

// Cell Types
export const fetchCellTypes = () => get<CellType[]>('/ufsinfo/cell-types');
export const createCellType = (data: Partial<CellType>) => post<CellType>('/ufsinfo/cell-types', data);
export const updateCellType = (id: number, data: Partial<CellType>) => put<CellType>(`/ufsinfo/cell-types/${id}`, data);
export const deleteCellType = (id: number) => del(`/ufsinfo/cell-types/${id}`);

// Controllers
export const fetchControllers = () => get<Controller[]>('/ufsinfo/controllers');
export const createController = (data: Partial<Controller>) => post<Controller>('/ufsinfo/controllers', data);
export const updateController = (id: number, data: Partial<Controller>) => put<Controller>(`/ufsinfo/controllers/${id}`, data);
export const deleteController = (id: number) => del(`/ufsinfo/controllers/${id}`);

// Densities
export const fetchDensities = () => get<Density[]>('/ufsinfo/densities');
export const createDensity = (data: Partial<Density>) => post<Density>('/ufsinfo/densities', data);
export const updateDensity = (id: number, data: Partial<Density>) => put<Density>(`/ufsinfo/densities/${id}`, data);
export const deleteDensity = (id: number) => del(`/ufsinfo/densities/${id}`);

// NAND Sizes
export const fetchNandSizes = () => get<NandSize[]>('/ufsinfo/nand-sizes');
export const createNandSize = (data: Partial<NandSize>) => post<NandSize>('/ufsinfo/nand-sizes', data);
export const updateNandSize = (id: number, data: Partial<NandSize>) => put<NandSize>(`/ufsinfo/nand-sizes/${id}`, data);
export const deleteNandSize = (id: number) => del(`/ufsinfo/nand-sizes/${id}`);

// NAND Types
export const fetchNandTypes = () => get<NandType[]>('/ufsinfo/nand-types');
export const createNandType = (data: Partial<NandType>) => post<NandType>('/ufsinfo/nand-types', data);
export const updateNandType = (id: number, data: Partial<NandType>) => put<NandType>(`/ufsinfo/nand-types/${id}`, data);
export const deleteNandType = (id: number) => del(`/ufsinfo/nand-types/${id}`);

// UFS Versions
export const fetchUfsVersions = () => get<UfsVersion[]>('/ufsinfo/ufs-versions');
export const createUfsVersion = (data: Partial<UfsVersion>) => post<UfsVersion>('/ufsinfo/ufs-versions', data);
export const updateUfsVersion = (id: number, data: Partial<UfsVersion>) => put<UfsVersion>(`/ufsinfo/ufs-versions/${id}`, data);
export const deleteUfsVersion = (id: number) => del(`/ufsinfo/ufs-versions/${id}`);

// OEMs
export const fetchOems = () => get<Oem[]>('/ufsinfo/oems');
export const createOem = (data: Partial<Oem>) => post<Oem>('/ufsinfo/oems', data);
export const updateOem = (id: number, data: Partial<Oem>) => put<Oem>(`/ufsinfo/oems/${id}`, data);
export const deleteOem = (id: number) => del(`/ufsinfo/oems/${id}`);
