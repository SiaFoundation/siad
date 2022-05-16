import { SWRError } from './types'

export async function handleResponse(response: Response | undefined) {
  // If the status code is not in the range 200-299,
  // we still try to parse and throw it.
  if (!response || !response.ok) {
    const message = await response?.text()
    const error: SWRError = new Error(
      message || 'An error occurred while fetching the data.'
    )
    // Attach extra info to the error object.
    error.status = response?.status || 500
    throw error
  } else {
    return response.json()
  }
}
