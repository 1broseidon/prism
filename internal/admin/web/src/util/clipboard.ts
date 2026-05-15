export function copyToClipboard(text: string, onCopied?: () => void): void {
  navigator.clipboard.writeText(text).then(() => {
    onCopied?.();
  });
}
