// Tests for the composer's attachment rules, extracted from App.svelte
// so they are reachable without a component harness.

import { describe, test, expect } from 'vitest';
import {
  rejectionReason,
  isImage,
  selectPriorAttachments,
  ATTACHMENT_MAX_IMAGE_BYTES,
  ATTACHMENT_MAX_DOC_BYTES,
  MAX_REFED_ATTACHMENTS,
} from './attachments';
import type { Message, StoredAttachment } from './store';

const file = (type: string, size: number, name = 'f') => ({ name, type, size });

describe('rejectionReason', () => {
  test('accepts the supported image types and PDF', () => {
    for (const t of ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'application/pdf']) {
      expect(rejectionReason(file(t, 1024))).toBe('');
    }
  });

  test('rejects an unsupported type, naming it', () => {
    expect(rejectionReason(file('image/tiff', 10))).toMatch(/Unsupported attachment type: image\/tiff/);
  });

  test('falls back to the filename when the browser reports no type', () => {
    expect(rejectionReason(file('', 10, 'mystery.xyz'))).toMatch(/mystery\.xyz/);
  });

  // Documents get a larger cap than images: a PDF's bytes become
  // extracted text rather than a per-pixel prompt cost.
  test('caps images at 10 MB and documents at 25 MB', () => {
    expect(rejectionReason(file('image/png', ATTACHMENT_MAX_IMAGE_BYTES))).toBe('');
    expect(rejectionReason(file('image/png', ATTACHMENT_MAX_IMAGE_BYTES + 1))).toMatch(/exceeds 10 MB/);

    expect(rejectionReason(file('application/pdf', ATTACHMENT_MAX_DOC_BYTES))).toBe('');
    expect(rejectionReason(file('application/pdf', ATTACHMENT_MAX_DOC_BYTES + 1))).toMatch(/exceeds 25 MB/);

    // A PDF between the two caps must not be judged by the image limit.
    expect(rejectionReason(file('application/pdf', ATTACHMENT_MAX_IMAGE_BYTES + 1))).toBe('');
  });

  test('names the offending file in a size rejection', () => {
    expect(rejectionReason(file('image/png', 99e6, 'huge.png'))).toContain('"huge.png"');
  });
});

test('isImage distinguishes images from documents', () => {
  expect(isImage('image/png')).toBe(true);
  expect(isImage('application/pdf')).toBe(false);
});

describe('selectPriorAttachments', () => {
  const att = (name: string, size: number): StoredAttachment => ({
    path: `/tmp/${name}`, name, mimeType: 'application/pdf', size,
  });
  const turn = (...as: StoredAttachment[]): Message =>
    ({ role: 'user', content: '', attachments: as }) as Message;

  test('returns nothing when no earlier turn carried an attachment', () => {
    expect(selectPriorAttachments([{ role: 'user', content: 'hi' } as Message])).toEqual([]);
    expect(selectPriorAttachments([])).toEqual([]);
  });

  test('returns them oldest-first so the model sees the original order', () => {
    const picked = selectPriorAttachments([turn(att('a', 1)), turn(att('b', 1)), turn(att('c', 1))]);
    expect(picked.map((a) => a.name)).toEqual(['a', 'b', 'c']);
  });

  test('keeps the newest when there are more than the count cap', () => {
    const turns = Array.from({ length: 12 }, (_, i) => turn(att(`f${i}`, 1)));
    const picked = selectPriorAttachments(turns);
    expect(picked).toHaveLength(MAX_REFED_ATTACHMENTS);
    // f11 is the most recent; the oldest kept is f4.
    expect(picked.map((a) => a.name)).toEqual(['f4','f5','f6','f7','f8','f9','f10','f11']);
  });

  test('stops at the byte cap rather than shipping the whole history', () => {
    const big = 10 * 1024 * 1024;
    const picked = selectPriorAttachments([turn(att('old', big)), turn(att('mid', big)), turn(att('new', big))]);
    // 24 MB budget fits two 10 MB files, taken newest-first.
    expect(picked.map((a) => a.name)).toEqual(['mid', 'new']);
  });

  test('a single oversized attachment is skipped, not truncated', () => {
    const picked = selectPriorAttachments([turn(att('enormous', 99 * 1024 * 1024))]);
    expect(picked).toEqual([]);
  });

  test('collects several attachments from one turn', () => {
    const picked = selectPriorAttachments([turn(att('a', 1), att('b', 1))]);
    expect(picked.map((a) => a.name).sort()).toEqual(['a', 'b']);
  });
});
