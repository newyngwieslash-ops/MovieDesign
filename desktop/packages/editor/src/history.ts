export class HistoryManager<T> {
  private undoStack: T[] = [];
  private redoStack: T[] = [];
  private capacity: number;
  constructor(capacity = 50) {
    if (!Number.isInteger(capacity) || capacity < 1) throw new RangeError('Invalid history capacity');
    this.capacity = capacity;
  }
  get canUndo(): boolean { return this.undoStack.length > 0; }
  get canRedo(): boolean { return this.redoStack.length > 0; }
  push(snapshot: T): void {
    this.undoStack.push(structuredClone(snapshot));
    if (this.undoStack.length > this.capacity) this.undoStack.shift();
    this.redoStack.length = 0;
  }
  undo(current: T): T | undefined {
    if (!this.canUndo) return undefined;
    this.redoStack.push(structuredClone(current));
    return this.undoStack.pop();
  }
  redo(current: T): T | undefined {
    if (!this.canRedo) return undefined;
    this.undoStack.push(structuredClone(current));
    return this.redoStack.pop();
  }
  clear(): void { this.undoStack.length = this.redoStack.length = 0; }
}
