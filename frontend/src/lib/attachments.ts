// Composer attachment handling: what may be attached, how a File
// becomes a pending attachment, and how prior-turn attachments are
// rehydrated for a follow-up request.
//
// Extracted from App.svelte so the validation and selection rules are
// testable without a component harness. The parts that touch browser
// APIs (FileReader, object URLs) stay thin wrappers around the pure
// rules above them.

import type { Message, StoredAttachment } from './store';

// PendingAttachment is a file staged for the next message. It carries
// already-base64-encoded bytes so the Wails IPC sees a clean string.
export interface PendingAttachment {
  id: string;
  name: string;
  mimeType: string;
  data: string; // base64, no data: prefix
  previewUrl: string; // blob URL for the thumbnail; revoked on remove
  size: number; // raw byte count for the size cap
}

// Anthropic / OpenAI vision / OpenRouter VL models all accept JPEG,
// PNG, WebP and GIF; this is the conservative intersection. PDFs ride
// the same path: Anthropic accepts them natively, OpenAI and OpenRouter
// get a Go-side text extraction.
export const IMAGE_MIME = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'];
export const DOCUMENT_MIME = ['application/pdf'];
export const ATTACHMENT_MIME_ACCEPT = [...IMAGE_MIME, ...DOCUMENT_MIME];

export const ATTACHMENT_MAX_IMAGE_BYTES = 10 * 1024 * 1024;
export const ATTACHMENT_MAX_DOC_BYTES = 25 * 1024 * 1024;

// rejectionReason returns the message to show for a file that cannot be
// attached, or '' when it is acceptable. Documents get a larger cap than
// images because a PDF's bytes become extracted text rather than a
// per-pixel prompt cost.
export function rejectionReason(file: { name: string; type: string; size: number }): string {
  if (!ATTACHMENT_MIME_ACCEPT.includes(file.type)) {
    return `Unsupported attachment type: ${file.type || file.name}. Allowed: PNG, JPEG, WebP, GIF, PDF.`;
  }
  const isDoc = DOCUMENT_MIME.includes(file.type);
  const cap = isDoc ? ATTACHMENT_MAX_DOC_BYTES : ATTACHMENT_MAX_IMAGE_BYTES;
  if (file.size > cap) {
    return `Attachment "${file.name}" exceeds ${isDoc ? '25 MB' : '10 MB'}.`;
  }
  return '';
}

export function isImage(mimeType: string): boolean {
  return mimeType.startsWith('image/');
}

// fileToBase64 strips the "data:<mime>;base64," prefix a FileReader
// data URL carries, leaving the payload the Go side expects.
export function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = reader.result as string;
      const i = result.indexOf(',');
      resolve(i >= 0 ? result.slice(i + 1) : result);
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

export async function toPendingAttachment(file: File): Promise<PendingAttachment> {
  return {
    id: `att-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
    name: file.name,
    mimeType: file.type,
    data: await fileToBase64(file),
    previewUrl: URL.createObjectURL(file),
    size: file.size,
  };
}

export function revokePreviews(list: PendingAttachment[]): void {
  for (const a of list) URL.revokeObjectURL(a.previewUrl);
}

// A long chat can accumulate more attachment bytes than any context
// window holds. The Go-side budget rewriter would trim the turns
// anyway, so there is no reason to ship the bytes across the IPC
// boundary first.
export const MAX_REFED_ATTACHMENTS = 8;
export const MAX_REFED_BYTES = 24 * 1024 * 1024;

// selectPriorAttachments picks which stored attachments from earlier
// turns to re-send, newest-first under the caps, then restores
// chronological order so the model sees them as the user added them.
//
// Pure: the caller does the actual reads. Separated because the
// selection rule is where the interesting behaviour is.
export function selectPriorAttachments(priorMessages: Message[]): StoredAttachment[] {
  const newestFirst: StoredAttachment[] = [];
  for (let i = priorMessages.length - 1; i >= 0; i--) {
    for (const a of priorMessages[i].attachments ?? []) newestFirst.push(a);
  }

  const picked: StoredAttachment[] = [];
  let bytes = 0;
  for (const a of newestFirst.slice(0, MAX_REFED_ATTACHMENTS)) {
    if (bytes + a.size > MAX_REFED_BYTES) break;
    picked.push(a);
    bytes += a.size;
  }
  return picked.reverse();
}
