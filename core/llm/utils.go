package llm

type DiffOps struct {
	ToRemove []string
	ToCreate [][2]*string // [previous_item_id, id]
	ToUpdate [][2]*string // [previous_item_id, id]
}

func ComputeChatCtxDiff(oldCtx, newCtx *ChatContext) *DiffOps {
	oldIDs := make([]string, len(oldCtx.Items))
	for i, item := range oldCtx.Items {
		oldIDs[i] = item.GetID()
	}

	newIDs := make([]string, len(newCtx.Items))
	for i, item := range newCtx.Items {
		newIDs[i] = item.GetID()
	}

	lcs := computeLCS(oldIDs, newIDs)
	lcsSet := make(map[string]struct{})
	for _, id := range lcs {
		lcsSet[id] = struct{}{}
	}

	diff := &DiffOps{}
	for _, id := range oldIDs {
		if _, ok := lcsSet[id]; !ok {
			diff.ToRemove = append(diff.ToRemove, id)
		}
	}

	var prevID *string
	for i, item := range newCtx.Items {
		id := item.GetID()
		if _, ok := lcsSet[id]; !ok {
			diff.ToCreate = append(diff.ToCreate, [2]*string{prevID, &id})
		} else {
			// Deep comparison for updates
			var oldItem, newItem ChatItem
			for _, o := range oldCtx.Items {
				if o.GetID() == id {
					oldItem = o
					break
				}
			}
			newItem = item
			if oldItem != nil && newItem != nil {
				if oMsg, ok := oldItem.(*ChatMessage); ok {
					if nMsg, ok := newItem.(*ChatMessage); ok {
						if oMsg.TextContent() != nMsg.TextContent() {
							diff.ToUpdate = append(diff.ToUpdate, [2]*string{prevID, &id})
						}
					}
				}
			}
		}
		newID := newIDs[i]
		prevID = &newID
	}

	return diff
}

func computeLCS(a, b []string) []string {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	var lcs []string
	i, j := n, m
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			lcs = append([]string{a[i-1]}, lcs...)
			i--
			j--
		} else if dp[i-1][j] > dp[i][j-1] {
			i--
		} else {
			j--
		}
	}
	return lcs
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

