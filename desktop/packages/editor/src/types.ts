export interface Point { x: number; y: number }
export interface Bounds extends Point { width: number; height: number }
export type ResizeCorner = 'nw' | 'ne' | 'sw' | 'se';

export interface ShapeStyle {
  stroke: string;
  strokeWidth: number;
  fill?: string;
  fontSize?: number;
  fontFamily?: string;
  fontWeight?: number | string;
  textVariant?: 'plain' | 'filled' | 'outlined';
  textOutlineColor?: string;
  tagBackground?: string;
  tagTextColor?: string;
  tagAnchorRadius?: number;
  mosaicMode?: 'mosaic' | 'blur';
  mosaicShape?: 'rectangle' | 'brush';
  mosaicBlockSize?: number;
  mosaicBrushSize?: number;
  blurRadius?: number;
  selectionStroke?: string;
  selectionLineDash?: number[];
  selectionHandleFill?: string;
  selectionHandleStroke?: string;
}

interface ShapeBase { id: string; style: ShapeStyle }
export interface RectangleData extends ShapeBase, Bounds { type: 'rectangle' }
export interface EllipseData extends ShapeBase, Bounds { type: 'ellipse' }
export interface LineData extends ShapeBase { type: 'line'; x1: number; y1: number; x2: number; y2: number }
export interface ArrowData extends ShapeBase { type: 'arrow'; x1: number; y1: number; x2: number; y2: number }
export interface BrushData extends ShapeBase { type: 'brush'; points: Point[] }
export interface TextData extends ShapeBase, Bounds { type: 'text'; text: string }
export interface TagData extends ShapeBase, Bounds { type: 'tag'; anchorX: number; anchorY: number; text: string; maxWidth?: number }
export type MosaicData = ShapeBase & { type: 'mosaic'; mode: 'mosaic' | 'blur'; strength: number } & (
  { shape: 'rectangle' } & Bounds | { shape: 'brush'; points: Point[]; brushSize: number }
);
export type ShapeData = RectangleData | EllipseData | LineData | ArrowData | BrushData | TextData | TagData | MosaicData;
export type ShapeType = ShapeData['type'];
