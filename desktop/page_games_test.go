// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "testing"

// A click on one of the player's own pieces first tries to move, so the rule
// engine has to reject the capture instead of overwriting the target.
func TestXiangqiMoveRejectsOwnPiece(t *testing.T) {
	var board [10][9]string
	board[9][0] = "rR"
	board[6][0] = "rP"
	if xiangqiMoveOK(board, 0, 9, 0, 6, "rR") {
		t.Fatal("rook moved onto its own pawn")
	}
	board[9][1] = "rR"
	if !xiangqiMoveOK(board, 1, 9, 1, 5, "rR") {
		t.Fatal("rook must still reach an empty square on the same file")
	}
}
