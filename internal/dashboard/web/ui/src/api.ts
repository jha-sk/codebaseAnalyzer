export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message)
  }
}

export async function fetchJSON<T>(path: string, token: string): Promise<T> {
  const resp = await fetch(path, { headers: { Authorization: `Bearer ${token}` } })
  if (!resp.ok) {
    throw new ApiError(`${path} returned ${resp.status}`, resp.status)
  }
  return (await resp.json()) as T
}
