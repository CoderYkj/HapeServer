package world

import "errors"

const maxSettledTrades = 1024

var (
	// ErrInsufficient means the player cannot cover the escrow.
	ErrInsufficient = errors.New("insufficient assets")
	// ErrTradeClosed means the trade already settled.
	ErrTradeClosed = errors.New("trade already settled")
	// ErrNoEscrow means the trade has no escrow on this player.
	ErrNoEscrow = errors.New("no escrow for trade")
)

// LockEscrow moves assets out of the player's available balance. It is
// idempotent for the same trade id.
func (p *PlayerActor) LockEscrow(tradeID string, a Assets) error {
	if a.Gold < 0 || a.Wood < 0 || a.Food < 0 {
		return ErrInsufficient
	}
	if p.State.Settled[tradeID] {
		return ErrTradeClosed
	}
	if _, ok := p.State.Escrow[tradeID]; ok {
		return nil
	}
	if p.State.Resources.Gold < a.Gold || p.State.Resources.Wood < a.Wood || p.State.Resources.Food < a.Food {
		return ErrInsufficient
	}
	p.State.Resources.Gold -= a.Gold
	p.State.Resources.Wood -= a.Wood
	p.State.Resources.Food -= a.Food
	p.State.Escrow[tradeID] = a
	p.State.Version++
	return nil
}

// RefundEscrow returns the player's own escrow. Refunding an already
// refunded/unknown trade is a no-op; refunding a settled trade is an error.
func (p *PlayerActor) RefundEscrow(tradeID string) error {
	if p.State.Settled[tradeID] {
		return ErrTradeClosed
	}
	a, ok := p.State.Escrow[tradeID]
	if !ok {
		return nil
	}
	p.State.Resources.Gold += a.Gold
	p.State.Resources.Wood += a.Wood
	p.State.Resources.Food += a.Food
	delete(p.State.Escrow, tradeID)
	p.State.Version++
	return nil
}

// SettleEscrow consumes the escrow and credits the assets received from the
// counterparty. Repeating the same trade id is a no-op.
func (p *PlayerActor) SettleEscrow(tradeID string, credit Assets) error {
	if p.State.Settled[tradeID] {
		return nil
	}
	if _, ok := p.State.Escrow[tradeID]; !ok {
		return ErrNoEscrow
	}
	delete(p.State.Escrow, tradeID)
	p.State.Resources.Gold += credit.Gold
	p.State.Resources.Wood += credit.Wood
	p.State.Resources.Food += credit.Food
	p.State.Settled[tradeID] = true
	if len(p.State.Settled) > maxSettledTrades {
		for k := range p.State.Settled {
			delete(p.State.Settled, k)
			break
		}
	}
	p.State.Version++
	return nil
}
