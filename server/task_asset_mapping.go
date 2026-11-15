package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Autumn-27/artex/db"
)

// taskAssetMapping starts a visible topology-mapping conversation for the task.
// The asset relationship graph remains sourced directly from the database and
// does not depend on this conversation. The conversation is the audit trail for
// the two human-readable reports (asset relationships + network topology).
func (s *Server) taskAssetMapping(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if s.m.pg == nil || s.chatAgentRef() == nil {
		writeErr(w, http.StatusServiceUnavailable, s.chatUnavailableReason())
		return
	}
	if agentInfo, err := s.m.pg.GetAgentByKey("asset_mapper"); err != nil || agentInfo == nil || !agentInfo.Enabled {
		writeErr(w, http.StatusServiceUnavailable, "Asset network topology Agent not enabled")
		return
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	message := fmt.Sprintf(`Please reorganize the network topology for task #%d and output two independent results in the same session.

The three zones are hard constraints for this task topology, not optional formatting:
- internet: public root domains, subdomains, public IPs, and external entry points.
- boundary: externally exposed services, gateways, web applications, or endpoints; include a node in this zone only when there is evidence of a public host/URL.
- internal: private IPs, loopback/link-local addresses, and the business applications, endpoints, and databases they host.
The zone/zone_reason in node summaries is the authoritative classification computed by the backend from real addresses and asset evidence; do not reassign zones based on kind alone. When zone is missing or evidence is insufficient, mark it as "To be confirmed" and do not fabricate cross-zone connections.

The output must strictly use the following two level-1 headings and must not be merged into one:
## Asset Relationship Results
- Based only on real asset data of the current task and its directly related tasks, preserve the hierarchy and ownership relationships in the asset library.
- This section explains the asset relationship graph; do not claim that this agent modified asset relationships, and do not add assets not present in the summary.

## Network Topology Results
- Only organize network relationships among internet entry points, boundary services, and internal business across the three zones, and state the zone of each node.
- Show only connections with real asset evidence; label database nodes with identifiable database types.

Both results must use only real asset data of the current task and its directly related tasks; do not probe targets, do not modify the asset library, and do not describe connections without evidence as verified paths.`, taskID)
	if as := s.m.Assets(); as != nil {
		if graph, err := as.BuildCoverageGraph(taskID, t.ExpID); err == nil {
			type nodeSample struct {
				Key, Kind, Label, Domain, IP, Zone, ZoneReason string
				Port                                           int
				Tested, InScope                                bool
			}
			counts := map[string]int{}
			sample := make([]nodeSample, 0, 60)
			for _, node := range graph.Nodes {
				counts[node.Kind]++
				if len(sample) < 60 {
					sample = append(sample, nodeSample{
						Key: node.Key, Kind: node.Kind, Label: node.Label, Domain: node.Domain,
						IP: node.IP, Port: node.Port, Tested: node.Tested, InScope: node.InScope,
						Zone: node.Zone, ZoneReason: node.ZoneReason,
					})
				}
			}
			edges := graph.Edges
			truncatedEdges := len(edges) > 80
			if truncatedEdges {
				edges = edges[:80]
			}
			snapshot, _ := json.Marshal(map[string]any{
				"nodes_total": len(graph.Nodes), "edges_total": len(graph.Edges),
				"counts_by_kind": counts, "node_sample": sample, "edge_sample": edges,
				"truncated": len(graph.Nodes) > len(sample) || truncatedEdges,
			})
			message += "\n\nThe following is the real summary of the task asset graph just read by the page; explain only based on the summary and task scope, and avoid mistaking global assets for assets of this task. Do not elaborate on nodes that are not listed:\n" + string(snapshot)
		}
	}
	title := fmt.Sprintf("Asset and Network Mapping · Task #%d", taskID)
	conversation, err := s.m.pg.CreateConversation("asset_mapper", title, nil)
	if err != nil || conversation == nil {
		writeErr(w, http.StatusInternalServerError, "Failed to create topology mapping session")
		return
	}
	if _, err := s.m.pg.AppendConvActivity(conversation.ID, db.Activity{
		Worker: "asset_mapper", Kind: "user", Summary: title, Detail: message,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to write topology mapping session")
		return
	}
	busyKey := s.convBusyKey(conversation.ID)
	s.chatMu.Lock()
	s.chatBusy[busyKey] = true
	s.chatMu.Unlock()
	s.runConversation(conversation, message, busyKey, message)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "accepted", "agent_key": "asset_mapper", "conversation_id": conversation.ID,
	})
}

func (s *Server) taskAssetMappingStatus(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	conversations, err := s.m.pg.ListConversations()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	title := fmt.Sprintf("Asset and Network Mapping · Task #%s", t.ID)
	legacyTitle := fmt.Sprintf("Asset Mapping · Task #%s", t.ID)
	for _, conversation := range conversations {
		if conversation.AgentKey != "asset_mapper" || (conversation.Title != title && conversation.Title != legacyTitle) {
			continue
		}
		busyKey := s.convBusyKey(conversation.ID)
		s.chatMu.Lock()
		running := s.chatBusy[busyKey]
		s.chatMu.Unlock()
		// A completed conversation is historical evidence, not an active run.
		// Keep looking so a newer run is not hidden by an older one, and avoid
		// making the client poll a completed conversation after page reload.
		if running {
			writeJSON(w, http.StatusOK, map[string]any{"running": true, "conversation_id": conversation.ID})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": false})
}
