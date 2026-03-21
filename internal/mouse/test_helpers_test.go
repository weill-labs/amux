package mouse

func (p *Parser) InProgress() bool {
	return p.state != stateNone
}
