// This file is fork code: the install dialog of the fork-plugin-packs
// experiment's organization page.

import { useFormik } from "formik";
import * as Yup from "yup";
import type { CreateForkPluginPackRequest } from "#/api/typesGenerated";
import { ErrorAlert } from "#/components/Alert/ErrorAlert";
import { Button } from "#/components/Button/Button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "#/components/Dialog/Dialog";
import { FormField } from "#/components/FormField/FormField";
import { Spinner } from "#/components/Spinner/Spinner";
import { getFormHelpers } from "#/utils/formUtils";

type ForkInstallPluginPackDialogProps = {
	error: unknown;
	isInstalling: boolean;
	onInstall: (req: CreateForkPluginPackRequest) => void;
	/** While isInstalling, close requests are ignored. */
	onClose: () => void;
};

type InstallFormValues = {
	name: string;
	source_url: string;
	source_path: string;
	ref: string;
};

const validationSchema = Yup.object({
	name: Yup.string().trim().required("Name is required."),
	source_url: Yup.string().trim().required("Git repository URL is required."),
	source_path: Yup.string(),
	ref: Yup.string().trim().required("Ref is required."),
});

export const ForkInstallPluginPackDialog: React.FC<
	ForkInstallPluginPackDialogProps
> = ({ error, isInstalling, onInstall, onClose }) => {
	const form = useFormik<InstallFormValues>({
		initialValues: { name: "", source_url: "", source_path: "", ref: "" },
		validationSchema,
		validateOnMount: true,
		onSubmit: (values) =>
			onInstall({
				name: values.name.trim(),
				source_url: values.source_url.trim(),
				source_path: values.source_path.trim() || undefined,
				ref: values.ref.trim(),
			}),
	});
	const getFieldHelpers = getFormHelpers(form, error);

	return (
		<Dialog
			open
			onOpenChange={(open) => {
				if (!open && !isInstalling) {
					onClose();
				}
			}}
		>
			<DialogContent>
				<DialogHeader>
					<DialogTitle>Install plugin pack</DialogTitle>
					<DialogDescription>
						Coder fetches the pack once and pins it to the commit the ref
						resolves to. The repository host must be on the organization's
						allowlist.
					</DialogDescription>
				</DialogHeader>
				<form
					className="flex flex-col gap-4"
					onSubmit={form.handleSubmit}
					noValidate
				>
					<FormField
						field={getFieldHelpers("name")}
						label="Name"
						placeholder="pstack"
						required
						disabled={isInstalling}
						autoFocus
					/>
					<FormField
						field={getFieldHelpers("source_url")}
						label="Git repository URL"
						placeholder="https://github.com/cursor/plugins.git"
						required
						disabled={isInstalling}
					/>
					<FormField
						field={getFieldHelpers("source_path")}
						label="Path"
						description="The plugin's folder in the repository. Leave empty when the plugin is at the root."
						placeholder="pstack"
						disabled={isInstalling}
					/>
					<FormField
						field={getFieldHelpers("ref")}
						label="Ref"
						placeholder="v0.15.36"
						required
						disabled={isInstalling}
					/>
					{Boolean(error) && <ErrorAlert error={error} />}
					<DialogFooter>
						<Button
							type="button"
							variant="outline"
							disabled={isInstalling}
							onClick={onClose}
						>
							Cancel
						</Button>
						<Button type="submit" disabled={!form.isValid || isInstalling}>
							<Spinner loading={isInstalling} />
							Install
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
};
