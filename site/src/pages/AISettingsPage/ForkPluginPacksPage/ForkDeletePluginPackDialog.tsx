// This file is fork code: the delete confirmation of the fork-plugin-packs
// experiment's organization page.

import type { ForkPluginPack } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { ConfirmDialog } from "#/components/Dialog/ConfirmDialog/ConfirmDialog";

type ForkDeletePluginPackDialogProps = {
	pack: ForkPluginPack;
	error: unknown;
	isDeleting: boolean;
	/** While isDeleting, close requests are ignored. */
	onClose: () => void;
	onCloseAutoFocus?: (event: Event) => void;
	onConfirm: () => void;
};

export const ForkDeletePluginPackDialog: React.FC<
	ForkDeletePluginPackDialogProps
> = ({ pack, error, isDeleting, onClose, onCloseAutoFocus, onConfirm }) => (
	<ConfirmDialog
		type="delete"
		open
		title={`Delete ${pack.name}?`}
		confirmText="Delete"
		description={
			<>
				<p>
					Projects and chats that enabled this pack lose its skills and agents.
				</p>
				{Boolean(error) && <ErrorAlert className="mt-3" error={error} />}
			</>
		}
		confirmLoading={isDeleting}
		onClose={() => {
			if (!isDeleting) {
				onClose();
			}
		}}
		onCloseAutoFocus={onCloseAutoFocus}
		onConfirm={onConfirm}
	/>
);
