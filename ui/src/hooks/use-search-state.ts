import { useSearchParams } from "wouter";

// A custom hook to manage a specific search parameter in the URL

// Overload: with parser, T can be any type
export function useSearchState<T>(
  key: string,
  defaultValue: T,
  parser: (initial: string | null) => T | null,
  stringify: (value: T) => string,
): [T, (newValue: T) => void];

// Overload: without parser, nullable default returns string | null
export function useSearchState(
  key: string,
  defaultValue: null,
): [string | null, (newValue: string | null) => void];

// Overload: without parser, undefined default returns string | undefined
export function useSearchState(
  key: string,
  defaultValue: undefined,
): [string | undefined, (newValue: string | undefined) => void];

// Overload: without parser, value is always string
export function useSearchState(
  key: string,
  defaultValue?: string,
): [string, (newValue: string) => void];

// Implementation
export function useSearchState<T = string>(
  key: string,
  defaultValue?: T,
  parser?: (initial: string | null) => T | null,
  stringify?: (value: T) => string,
): [T, (newValue: T) => void] {
  const [searchParams, setSearchParams] = useSearchParams();
  const rawValue = searchParams.get(key);
  const value = (parser ? parser(rawValue) : rawValue) ?? defaultValue;

  const setValue = (newValue: T) => {
    setSearchParams((prev) => {
      if (
        newValue === undefined ||
        newValue === null ||
        newValue === defaultValue
      ) {
        prev.delete(key);
      } else {
        prev.set(key, stringify ? stringify(newValue) : String(newValue));
      }
      return prev;
    });
  };

  return [value as T, setValue];
}

function parseNumber(value: string | null): number | null {
  if (value === null) return null;
  const parsed = Number(value);
  return Number.isNaN(parsed) ? null : parsed;
}

export function useSearchNumberState(
  key: string,
  defaultValue: number,
): [number, (newValue: number) => void] {
  return useSearchState<number>(key, defaultValue, parseNumber, String);
}
