package v1

import "net/http"

type approvalResponse struct {
	ApprovalID string `json:"approval_id"`
	Status     string `json:"status"`
}

// getApprovalForIntent is what the Approval UI (mandate §41) calls to
// render "Agent requesting purchase..." before showing Approve/Reject.
func (a *API) getApprovalForIntent(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	appr, err := a.b.Approvals.GetByIntent(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appr)
}

// approveApproval, rejectApproval, and reapproveApproval are the actual
// "Approve" / "Reject" buttons behind the mandate's Approval UI (§41). They
// are user actions — see currentUserID's doc comment on why that's a
// dev-mode placeholder here rather than a real session.
func (a *API) approveApproval(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	approved, err := a.b.Approvals.Approve(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, approvalResponse{ApprovalID: approved.ID, Status: string(approved.Status)})
}

func (a *API) rejectApproval(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	rejected, err := a.b.Approvals.Reject(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, approvalResponse{ApprovalID: rejected.ID, Status: string(rejected.Status)})
}

func (a *API) reapproveApproval(w http.ResponseWriter, r *http.Request) {
	userID, err := a.currentUserID(r)
	if err != nil {
		writeError(w, err)
		return
	}
	reapproved, err := a.b.Approvals.Reapprove(r.Context(), userID, r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, approvalResponse{ApprovalID: reapproved.ID, Status: string(reapproved.Status)})
}
