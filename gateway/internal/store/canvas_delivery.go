package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
)

type DeliveredItem struct{ ResourceID, VersionID, OutputKey string }

// PlaceDelivery is part of the Delivery commit. It uses the ordinary command
// ledger, preserving layout and frontend pending-node identities atomically.
func (c Canvas) PlaceDelivery(ctx context.Context, tx *sql.Tx, p, key, composition, request string, items []DeliveredItem) error {
	d, e := readCanvas(ctx, tx, p)
	if e != nil {
		return e
	}
	changes := []any{}
	nextX := 0.0
	for _, n := range d.Nodes {
		pos := n["position"].(map[string]any)
		size := n["size"].(map[string]any)
		nextX = max(nextX, pos["x"].(float64)+size["width"].(float64)+40)
	}
	for _, item := range items {
		var before map[string]any
		var id string
		for nid, node := range d.Nodes {
			if pending, ok := node["pendingGeneration"].(map[string]any); ok && pending["requestId"] == request && pending["outputKey"] == item.OutputKey {
				id = nid
				before = node
				break
			}
			if node["kind"] == "composition" && node["compositionId"] == composition {
				if active, ok := node["activeItem"].(map[string]any); ok && active["resourceId"] == item.ResourceID {
					id = nid
					before = node
					break
				}
			}
		}
		var node map[string]any
		if before != nil {
			node = maps.Clone(before)
			delete(node, "expectedKind")
			delete(node, "pendingGeneration")
		} else {
			id = "delivery-" + item.ResourceID
			if d.Nodes[id] != nil {
				return canvasInvalid("Delivery node identity conflicts")
			}
			// ponytail: append at the rightmost edge; replace with user placement only if
			// automatic layout becomes a requested feature.
			node = map[string]any{"id": id, "parentId": nil, "position": map[string]any{"x": nextX, "y": 0}, "size": map[string]any{"width": 360, "height": 280}, "zIndex": len(d.Nodes) + len(changes), "hidden": false}
			nextX += 400
		}
		// Do not move selection backwards when an older reserved output arrives late.
		var current string
		if e = tx.QueryRowContext(ctx, "SELECT current_version_id FROM canvas_resource_state WHERE resource_id=?", item.ResourceID).Scan(&current); e != nil {
			return e
		}
		if before != nil && current != item.VersionID {
			continue
		}
		node["kind"] = "composition"
		node["compositionId"] = composition
		node["draft"] = nil
		node["activeItem"] = map[string]any{"type": "version", "resourceId": item.ResourceID, "versionId": item.VersionID}
		var old any
		if before != nil {
			old = before
		}
		changes = append(changes, map[string]any{"id": id, "before": old, "after": node})
	}
	if len(changes) == 0 {
		return nil
	}
	raw, _ := json.Marshal(map[string]any{"requestId": "delivery-" + key, "expectedRevision": d.Revision, "command": map[string]any{"type": "canvas.patch", "nodes": changes, "edges": []any{}}})
	_, e = c.ExecuteTx(ctx, tx, p, raw)
	return e
}
