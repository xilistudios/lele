/** Fetcher interface: avoids coupling this module to the full ApiClient. */
export type FileBlobFetcher = {
  fileBlob: (path: string, signal?: AbortSignal) => Promise<Blob>
}

/**
 * Creates a temporary `<a download>` with an object URL and clicks it.
 * Always revokes the object URL to avoid memory leaks.
 */
export function triggerBlobDownload(blob: Blob, name?: string): void {
  const url = URL.createObjectURL(blob)
  try {
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = name ?? 'download'
    anchor.style.display = 'none'
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}

/**
 * Downloads a file from the backend through the authenticated endpoint.
 *
 * The public "/api/v1/files/view" cannot serve workspace-persisted attachments
 * (403), and a plain href cannot carry Authorization, so the caller must
 * fetch the blob first and then trigger the download via object URL.
 */
export async function downloadFileViaApi(
  fetcher: FileBlobFetcher,
  path: string,
  name?: string,
): Promise<void> {
  triggerBlobDownload(await fetcher.fileBlob(path), name)
}
