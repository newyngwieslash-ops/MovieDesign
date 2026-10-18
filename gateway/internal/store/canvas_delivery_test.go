package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestDeliveryCanvasOutputIdentity(t *testing.T) {
	for _, mode := range []string{"pending-first", "two-candidates", "older-pending", "older-unplaced", "two-unplaced", "two-existing"} {
		t.Run(mode, func(t *testing.T) {
			s, p, _ := objectFixture(t)
			ctx := context.Background()
			c := Canvas{DB: s.DB}
			typ := objectID(t, mutateObject(t, s, p, "composition_types", "type", "create", "", `{"label":"Image"}`))
			comp := objectID(t, mutateObject(t, s, p, "compositions", "comp", "create", "", fmt.Sprintf(`{"type_id":%q,"name":"Object"}`, typ)))
			resource := objectID(t, mutateObject(t, s, p, "resources", "resource", "create", "", fmt.Sprintf(`{"composition_id":%q}`, comp)))
			versions := []string{}
			for i := 0; i < 3; i++ {
				versions = append(versions, objectID(t, mutateObject(t, s, p, "versions", fmt.Sprint("version", i), "create", "", fmt.Sprintf(`{"resource_id":%q}`, resource))))
			}
			nodes := []any{}
			ordinary := canvasFrame("existing", 30)
			delete(ordinary, "title")
			delete(ordinary, "collapsed")
			ordinary["kind"] = "composition"
			ordinary["compositionId"] = comp
			ordinary["draft"] = nil
			ordinary["activeItem"] = map[string]any{"type": "version", "resourceId": resource, "versionId": versions[2]}
			if mode != "two-unplaced" {
				nodes = append(nodes, map[string]any{"id": "existing", "before": nil, "after": ordinary})
			}
			addPending := func(key string, x int) {
				n := canvasFrame(key, x)
				delete(n, "title")
				delete(n, "collapsed")
				n["kind"] = "placeholder"
				n["expectedKind"] = "image"
				n["draft"] = nil
				n["pendingGeneration"] = map[string]any{"requestId": "intent", "outputKey": key}
				nodes = append(nodes, map[string]any{"id": key, "before": nil, "after": n})
			}
			items := []DeliveredItem{{resource, versions[2], "one"}}
			if mode == "pending-first" {
				addPending("one", 900)
			}
			if mode == "older-pending" || mode == "older-unplaced" {
				items[0].VersionID = versions[1]
				if mode == "older-pending" {
					addPending("one", 900)
				}
			}
			if mode == "two-candidates" || mode == "two-unplaced" || mode == "two-existing" {
				items = []DeliveredItem{{resource, versions[1], "one"}, {resource, versions[2], "two"}}
				if mode == "two-candidates" {
					addPending("one", 900)
					addPending("two", 1300)
				}
			}
			if len(nodes) > 0 {
				raw, _ := json.Marshal(map[string]any{"requestId": "setup", "expectedRevision": 0, "command": map[string]any{"type": "canvas.patch", "nodes": nodes, "edges": []any{}}})
				if _, e := c.Execute(ctx, p, raw); e != nil {
					t.Fatal(e)
				}
			}
			before, e := c.Document(ctx, p)
			if e != nil {
				t.Fatal(e)
			}
			// Repeated rollback trials expose map-order dependent resource stealing.
			trials := 1
			if mode == "pending-first" {
				trials = 32
			}
			for trial := 0; trial < trials; trial++ {
				tx, e := s.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				if e = c.PlaceDelivery(ctx, tx, p, "delivery", comp, "intent", items); e != nil {
					tx.Rollback()
					t.Fatal(e)
				}
				got, e := readCanvas(ctx, tx, p)
				if e != nil {
					tx.Rollback()
					t.Fatal(e)
				}
				if mode != "two-unplaced" && !reflect.DeepEqual(got.Nodes["existing"], before.Nodes["existing"]) {
					tx.Rollback()
					t.Fatal("unrelated resource selection changed")
				}
				for _, item := range items {
					id := item.OutputKey
					if before.Nodes[id] == nil {
						id = "delivery-" + item.VersionID
					}
					n := got.Nodes[id]
					if n == nil || n["pendingGeneration"] != nil || n["activeItem"].(map[string]any)["versionId"] != item.VersionID {
						tx.Rollback()
						t.Fatalf("output %s lost its version/node: %+v", item.OutputKey, n)
					}
					if old := before.Nodes[id]; old != nil && !reflect.DeepEqual(n["position"], old["position"]) {
						tx.Rollback()
						t.Fatal("pending layout moved")
					}
				}
				tx.Rollback()
			}
		})
	}
}
