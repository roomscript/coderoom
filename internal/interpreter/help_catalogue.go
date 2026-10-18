package interpreter

func (i *Interpreter) readHelp() HelpListed {
	var listing HelpListed
	for _, command := range i.commands.Entries() {
		listing.Commands = append(listing.Commands, HelpEntry{
			Usage: command.Usage(), Description: command.Description(),
		})
	}
	return listing
}
