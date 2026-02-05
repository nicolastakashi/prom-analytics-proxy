import { useSearchParams } from "wouter";

// A custom hook to manage a specific search parameter in the URL
// TODO: handle debounce if needed + remove from state if value is equal to defaultValue
export function useSearchState<T>(
  key: string,
  defaultValue?: T,
  parser?: (initial: string | null) => T | null,
  stringify?: (value: T) => string,
): [T, (newValue: T) => void] {
  const [searchParams, setSearchParams] = useSearchParams();
  const value =
    (parser ? parser(searchParams.get(key)) : (searchParams.get(key) as T)) ??
    defaultValue;

  const setValue = (newValue: T) => {
    setSearchParams((prev) => {
      if (
        newValue === undefined ||
        newValue === null ||
        newValue === defaultValue
      ) {
        prev.delete(key);
      } else {
        prev.set(
          key,
          stringify ? stringify(newValue) : (newValue as unknown as string),
        );
      }
      return prev;
    });
  };

  return [value as T, setValue];
}
