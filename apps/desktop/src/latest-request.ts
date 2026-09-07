/** Only the latest request in a mounted view may publish a result or error. */
export class LatestRequest {
  private generation = 0;

  invalidate(): void {
    this.generation++;
  }

  async run<T>(request: () => Promise<T>, accept: (value: T) => void, reject: (error: unknown) => void): Promise<void> {
    const generation = ++this.generation;
    try {
      const value = await request();
      if (generation === this.generation) accept(value);
    } catch (error) {
      if (generation === this.generation) reject(error);
    }
  }
}
