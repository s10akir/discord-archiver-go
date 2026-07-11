import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)); }
export function formatBytes(size:number){if(size<1024)return `${size} B`;const units=["KiB","MiB","GiB","TiB"];let value=size/1024,i=0;while(value>=1024&&i<units.length-1){value/=1024;i++}return `${value.toFixed(1)} ${units[i]}`}
